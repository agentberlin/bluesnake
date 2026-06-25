# Windows code signing — reference & how-to (NOT yet implemented)

> **Status:** Windows artifacts ship **unsigned** today. This document is the
> plan/reference for when we decide to add Authenticode signing — it has all the
> context, current options, costs, exact steps, and the workflow changes needed,
> so a future session (or a future you) can act on it cold. Researched & verified
> June 2026; **re-verify the volatile bits** (prices, action names, country
> eligibility, deprecation dates) at implementation time — see
> [Re-verify before you start](#re-verify-before-you-start).

macOS is already Developer ID signed + notarized (see [PACKAGING.md → Signing](PACKAGING.md#signing));
Windows is the remaining gap. The two are **completely independent** — the Apple
Developer Program membership does nothing for Windows.

---

## 1. Context (this repo)

- bluesnake's desktop app is **Wails v2**. The `desktop-windows` job in
  [`.github/workflows/release.yml`](../.github/workflows/release.yml) runs on a
  **GitHub-hosted `windows-latest` runner** and produces two artifacts:
  - a **portable `.exe`** (`wails build -platform windows/amd64`) →
    `bluesnake-<ver>-windows-amd64.exe`
  - an **NSIS installer** (`wails build … -nsis`, currently best-effort) →
    `bluesnake-<ver>-windows-amd64-installer.exe`
- The signer is an **individual developer with no registered company** (no LLC,
  no D-U-N-S number).
- Signing must happen **in CI on a hosted runner** — so **a physical USB token is
  not an option** (you can't plug hardware into a GitHub-hosted runner). This single
  constraint rules out most "classic" code-signing products and points squarely at
  **cloud signing services**.

---

## 2. What changed in 2023–2026 (why this isn't "buy a cert, download a .pfx")

Two industry shifts make almost every pre-2023 tutorial obsolete:

1. **Hardware-key mandate (effective 1 June 2023).** The
   [CA/Browser Forum Code Signing Baseline Requirements](https://cabforum.org/working-groups/code-signing/requirements/)
   require *all* code-signing private keys (OV **and** EV) to be generated and held,
   non-exportably, on **FIPS 140-2 Level 2+ / Common Criteria EAL4+ hardware**. CAs
   **no longer let you download a `.pfx`/PKCS#12 file.** Your only real choices are:
   - a **physical USB token** (shipped to you) — *unusable on hosted CI runners*, or
   - a **cloud HSM / cloud-signing service** that holds the key and signs over an API.
2. **EV no longer skips SmartScreen (since ~March 2024).** An Extended Validation
   cert used to grant *instant* "no warning" SmartScreen reputation. Microsoft removed
   that — per [Microsoft Learn: SmartScreen reputation](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation),
   **OV and EV now build reputation identically** over download history, and Microsoft
   explicitly says paying the EV premium *just* to avoid SmartScreen "is no longer
   justified."

**Conclusion for us:** use a **cloud-signing service**, and **don't chase EV**.

---

## 3. Options at a glance

| Option | Who can get it (individual) | ~Cost | CI fit | Trust tier | Notes |
|---|---|---|---|---|---|
| **Azure Artifact Signing** (formerly "Trusted Signing") | **US / Canada only** | **~$10/mo** (+ paid Azure sub) | ★★★ first-party Action, OIDC | OV | **Recommended.** No token; MS cloud HSM. |
| **SSL.com Individual Validation + eSigner** | Worldwide | ~$129/yr cert + ~$20+/mo eSigner | ★★★ official Action | OV/IV | Closed-source OK. The worldwide fallback. |
| **Certum Open Source Code Signing** | Worldwide, **OSS projects only** | ~$50–58/yr | ★ awkward headless | OV | Must supply a public project URL; SimplySign needs TOTP/container hacks in CI. |
| **DigiCert KeyLocker** | Worldwide | ~$400/yr | ★★ good | OV | Most polished, priciest. Use the *new* `digicert/ssm-code-signing` action. |
| **EV (any CA)** | Needs a business / sole-proprietorship | $$$ + token/HSM | ✗ | EV | No longer skips SmartScreen → **not worth it.** |

---

## 4. Recommended path — Azure Artifact Signing

Microsoft's cloud signing service. Originally "Azure Code Signing" → "Trusted
Signing" → **renamed "Azure Artifact Signing"**, **GA January 2026**. (You'll see
all three names in docs/actions — same service.)

**Why it fits us:**
- **~$9.99/month** Basic tier (5,000 signatures); needs a **paid** Azure
  subscription (free/trial/sponsored are rejected; billing is **not** pro-rated).
- **Individual developers are eligible** — *no company required*. Identity is
  validated with a **government photo ID + selfie/FaceCheck** via Microsoft Entra
  Verified ID (the same flavor of personal verification as Apple). The old
  "organization must be 3+ years old" rule was **org-only** and does not apply.
- **No hardware token** — keys live in Microsoft's **FIPS 140-2 Level 3 cloud HSM**,
  satisfying the 2023 mandate, and signing is an authenticated API call that works
  on a hosted runner.
- **First-party GitHub Action** with **OIDC** — no key material or long-lived
  secret on the runner.

**The one real gate:** for **Public Trust** certs, individual eligibility is
**USA & Canada only** right now (organizations also get EU/UK). If you're outside
US/Canada as an individual, **skip to [§5 SSL.com](#5-fallback-outside-uscanada--sslcom-iv--esigner).**
Verify current country list in the [Artifact Signing FAQ](https://learn.microsoft.com/en-us/azure/artifact-signing/faq).

**Other things to know:**
- It is **OV-tier** — Microsoft **does not and will not issue EV**.
- The minted certs are **short-lived (~3 days)**, so you **must RFC3161-timestamp**
  (`http://timestamp.acs.microsoft.com`) or signatures "expire" almost immediately.
- The publisher shown to users is your **validated legal name** + city/state/country.

### 4a. One-time account setup

1. **Paid Azure subscription** (pay-as-you-go) with billing **Account Type =
   Individual**. Make the billing legal name + address **exactly match your
   government ID** — these auto-populate the certificate subject.
2. Register the provider and CLI extension:
   ```sh
   az provider register --namespace Microsoft.CodeSigning
   az extension add --name artifact-signing
   ```
3. **Create an Artifact Signing account** (Basic SKU) in a supported region:
   ```sh
   az artifact-signing create -n <AccountName> -l eastus -g <ResourceGroup> --sku Basic
   ```
4. Assign yourself the **"Artifact Signing Identity Verifier"** role, then in the
   Azure portal create an **Individual → Public** identity validation and complete
   the **Entra Verified ID / FaceCheck** flow (Microsoft Authenticator app + a
   government photo ID; address must match). **This review takes 1–20 business days**
   — like Apple's.
5. Once validation status = **Completed**, create a **Public Trust certificate
   profile**.
6. Create a **Microsoft Entra app registration** + a **GitHub OIDC federated
   credential**, and grant that service principal the **"Artifact Signing
   Certificate Profile Signer"** role **scoped to the certificate profile / resource
   group** (not your personal user — a common 403 cause).

### 4b. GitHub Actions wiring (in `desktop-windows`)

GitHub **secrets** needed (these are just IDs, not credentials — OIDC does the auth):
`AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID`.

Job needs `permissions: { id-token: write, contents: read }`, then:

```yaml
      - uses: azure/login@v3
        with:
          client-id: ${{ secrets.AZURE_CLIENT_ID }}
          tenant-id: ${{ secrets.AZURE_TENANT_ID }}
          subscription-id: ${{ secrets.AZURE_SUBSCRIPTION_ID }}

      - uses: azure/trusted-signing-action@v2   # may be azure/artifact-signing-action@v2 — check README
        with:
          endpoint: https://eus.codesigning.azure.net/        # match your account region
          trusted-signing-account-name: <AccountName>          # newer: signing-account-name
          certificate-profile-name: <ProfileName>
          files-folder: desktop/build/bin
          files-folder-filter: exe
          file-digest: SHA256
          timestamp-rfc3161: http://timestamp.acs.microsoft.com
          timestamp-digest: SHA256
```

Run the signing step **twice** (or point `files-folder` appropriately): once on the
**app `.exe`** and once on the **installer `.exe`** — see [§7 Wails/NSIS](#7-wails--nsis-signing-specifics-important).

References: [Quickstart](https://learn.microsoft.com/en-us/azure/artifact-signing/quickstart),
[FAQ](https://learn.microsoft.com/en-us/azure/artifact-signing/faq),
[Action + OIDC docs](https://github.com/Azure/trusted-signing-action/blob/main/docs/OIDC.md),
[pricing](https://azure.microsoft.com/en-us/pricing/details/artifact-signing/).

---

## 5. Fallback (outside US/Canada) — SSL.com IV + eSigner

If you can't use Artifact Signing as an individual:

- Buy an **Individual Validation (IV)** code-signing cert (~**$129/yr**) — validated
  on a **government ID only, no company** ([SSL.com IV](https://www.ssl.com/products/software-integrity/code-signing/iv/)).
- Add an **eSigner cloud-signing subscription** (~**$20+/mo**, 30-day free trial) —
  the key lives in SSL.com's cloud HSM (mandate-compliant), signed via their CLI.
- Works **worldwide** and for **closed-source** apps, with an
  [official GitHub Action](https://www.ssl.com/how-to/cloud-code-signing-integration-with-github-actions/).

GitHub **secrets**: `ES_USERNAME`, `ES_PASSWORD`, `CREDENTIAL_ID`, `ES_TOTP_SECRET`
(the TOTP secret is what makes signing non-interactive).

```yaml
      - uses: sslcom/actions-codesigner@develop
        with:
          command: sign            # use batch_sign to sign multiple files with one OTP
          username: ${{ secrets.ES_USERNAME }}
          password: ${{ secrets.ES_PASSWORD }}
          credential_id: ${{ secrets.CREDENTIAL_ID }}
          totp_secret: ${{ secrets.ES_TOTP_SECRET }}
          file_path: desktop/build/bin/bluesnake.exe
          output_path: signed/
```

Again: invoke once for the app `.exe`, once for the installer `.exe`.

---

## 6. Other options (brief)

- **Certum "Open Source Code Signing"** — cheapest (~$50–58/yr) and trusted by
  Microsoft, but **only issuable for genuine open-source projects** (you must supply a
  public project URL + proof of involvement), and its **SimplySign** tool is built for
  interactive use — headless CI needs workarounds (scripting the TOTP from the
  `otpauth` QR, or a Linux `p11-kit` container). Only consider if bluesnake is OSS.
- **DigiCert KeyLocker** — cloud HSM (FIPS 140-2 L3), first-class CI support, but
  ~$400/yr. **Use the newer `digicert/ssm-code-signing` action** — the old Software
  Trust Manager signing path is **deprecated (EOS 1 Mar 2026, retired 1 May 2026)**.

---

## 7. Why not EV

- A bare individual **cannot** get an EV cert — EV requires an organization or a
  **sole-proprietorship** with a Dun & Bradstreet/government business listing and a
  notarized identity form. Heavy.
- EV historically required a physical token (bad for hosted CI; cloud-HSM EV exists
  but costs more).
- **It no longer buys instant SmartScreen reputation** (changed ~March 2024). So the
  whole reason people paid for EV is gone. **Skip it.**

---

## 8. SmartScreen reality (set expectations)

- Signing **immediately** removes "**Unknown publisher**" and shows your **verified
  name** in the UAC/SmartScreen dialog.
- It does **not** instantly remove the "Windows protected your PC / unrecognized app"
  warning. SmartScreen **reputation builds automatically with download volume** — per
  Microsoft, *"several weeks and hundreds of clean installs from a wide audience."*
  There is **no** way to submit a file to fast-track consumer SmartScreen.
- **Sign every release with the *same* identity** so certificate-level reputation
  carries across versions. For a low-volume indie app, expect warnings early on; they
  fade as installs accumulate. (The only true instant-trust path is the Microsoft
  Store, which re-signs apps.)

---

## 9. Wails / NSIS signing specifics (important)

**Wails does NOT sign the application `.exe`** — its `-nsis` flag only signs the
*installer* and *uninstaller* (via `!finalize` / `!uninstfinalize` hooks). See
[wailsapp/wails#3716](https://github.com/wailsapp/wails/issues/3716) and the
[Wails signing guide](https://wails.io/docs/guides/signing/). So you must sign the
binaries yourself, in this order:

1. `wails build -platform windows/amd64` → **sign the portable app `.exe`**
   (`desktop/build/bin/bluesnake.exe`).
2. Build the NSIS installer **so the signed app `.exe` is the one bundled inside**.
   (Either configure the NSIS `!finalize` hook to call your signing tool, or restructure
   so the app exe is signed before packaging — note `wails build -nsis` rebuilds the
   exe, so signing-before-package needs care.)
3. **Sign the produced installer `.exe`** (`desktop/build/bin/*installer*.exe`).

A standard (non-EV) cert is sufficient for Wails. Always **timestamp**.

---

## 10. Checklist (when you're ready)

- [ ] Confirm your country → US/Canada: **Azure Artifact Signing** (§4); else **SSL.com IV** (§5).
- [ ] Stand up the account + complete **identity validation** (budget 1–20 business days).
- [ ] Add the GitHub **secrets** for the chosen path.
- [ ] Add signing steps to the `desktop-windows` job — sign **app `.exe`** and
      **installer `.exe`**, with RFC3161 timestamping.
- [ ] Make signing **required / fail-loud** (consistent with the macOS job — no unsigned fallback).
- [ ] Update [PACKAGING.md](PACKAGING.md#signing): move Windows from "not done" → "done".
- [ ] Verify a built artifact: `signtool verify /pa /v bluesnake-<ver>-windows-amd64.exe`
      shows your publisher and a valid timestamp.
- [ ] Accept that SmartScreen warnings persist until reputation accrues (§8).

---

## Re-verify before you start

This space moves fast — confirm these at implementation time:

- **Price** of Artifact Signing (was ~$9.99/mo) — [Azure pricing](https://azure.microsoft.com/en-us/pricing/details/artifact-signing/).
- **Action name & inputs** — `azure/trusted-signing-action` vs `azure/artifact-signing-action@v2`;
  `trusted-signing-account-name` vs `signing-account-name`. Check the action README.
- **Country eligibility** for individual Public Trust certs — [FAQ](https://learn.microsoft.com/en-us/azure/artifact-signing/faq).
- **DigiCert** Software Trust Manager deprecation (EOS 1 Mar 2026) if going that route.
- **CA/B Forum** max cert validity (dropping to ~460 days from 1 Mar 2026) — affects renewal cadence.

## Sources

- [Microsoft Learn — Azure Artifact Signing FAQ](https://learn.microsoft.com/en-us/azure/artifact-signing/faq) (updated 2026-06)
- [Microsoft Learn — Artifact Signing Quickstart](https://learn.microsoft.com/en-us/azure/artifact-signing/quickstart)
- [Microsoft Learn — SmartScreen reputation for Windows app developers](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation) (updated 2026-05; EV no longer bypasses SmartScreen)
- [Azure/trusted-signing-action — OIDC docs](https://github.com/Azure/trusted-signing-action/blob/main/docs/OIDC.md)
- [Artifact Signing GA announcement](https://techcommunity.microsoft.com/blog/microsoft-security-blog/simplifying-code-signing-for-windows-apps-artifact-signing-ga/4482789) (Jan 2026)
- [CA/Browser Forum — Code Signing Baseline Requirements](https://cabforum.org/working-groups/code-signing/requirements/) (hardware-key mandate, in force since 2023-06-01)
- [SSL.com — Individual Validation code signing](https://www.ssl.com/products/software-integrity/code-signing/iv/) + [GitHub Actions integration](https://www.ssl.com/how-to/cloud-code-signing-integration-with-github-actions/)
- [Certum — Open Source Code Signing](https://certum.store/open-source-code-signing-on-simplysign.html)
- [DigiCert KeyLocker — GitHub Actions](https://docs.digicert.com/en/digicert-keylocker/ci-cd-integrations-and-deployment-pipelines/plugins/github/binary-signing-using-github-actions.html)
- [wailsapp/wails#3716 — Wails doesn't sign the app exe](https://github.com/wailsapp/wails/issues/3716) · [Wails signing guide](https://wails.io/docs/guides/signing/)
