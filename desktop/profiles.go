package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/store"
	"gopkg.in/yaml.v3"
)

// Profiles are named config files under <store-dir>/profiles/<slug>.yaml.
// The crawl freezes whichever profile it started from (store.CreateCrawl
// already persists the config), so editing a profile never mutates past
// crawls.

const defaultProfile = "Default audit"

func (a *App) profilesDir() string { return filepath.Join(a.storeDir, "profiles") }

func profileSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, s)
	return strings.Trim(s, "-")
}

func (a *App) profilePath(name string) string {
	return filepath.Join(a.profilesDir(), profileSlug(name)+".yaml")
}

// ensureDefaultProfile writes the built-in defaults on first run.
func (a *App) ensureDefaultProfile() error {
	if err := os.MkdirAll(a.profilesDir(), 0o755); err != nil {
		return err
	}
	p := a.profilePath(defaultProfile)
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	data, err := yaml.Marshal(config.Default())
	if err != nil {
		return err
	}
	header := "# " + defaultProfile + "\n"
	return os.WriteFile(p, append([]byte(header), data...), 0o644)
}

func (a *App) loadProfileConfig(name string) (*config.Config, error) {
	if name == "" || name == defaultProfile {
		if err := a.ensureDefaultProfile(); err != nil {
			return config.Default(), nil
		}
	}
	path := a.profilePath(name)
	if name == "" {
		path = a.profilePath(defaultProfile)
	}
	if _, err := os.Stat(path); err != nil {
		return config.Default(), nil
	}
	return config.LoadFile(path)
}

func (a *App) ListProfiles() ([]string, error) {
	if err := a.ensureDefaultProfile(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(a.profilesDir())
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		names = append(names, profileDisplayName(filepath.Join(a.profilesDir(), e.Name())))
	}
	sort.SliceStable(names, func(i, j int) bool {
		if names[i] == defaultProfile {
			return true
		}
		if names[j] == defaultProfile {
			return false
		}
		return names[i] < names[j]
	})
	return names, nil
}

// profileDisplayName reads the "# Name" header comment, falling back to the
// slug.
func profileDisplayName(path string) string {
	data, err := os.ReadFile(path)
	if err == nil {
		first, _, _ := strings.Cut(string(data), "\n")
		if strings.HasPrefix(first, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(first, "# "))
		}
	}
	base := strings.TrimSuffix(filepath.Base(path), ".yaml")
	return strings.ReplaceAll(base, "-", " ")
}

func (a *App) DuplicateProfile(src, dst string) error {
	if profileSlug(dst) == "" {
		return fmt.Errorf("profile name required")
	}
	data, err := os.ReadFile(a.profilePath(src))
	if err != nil {
		return err
	}
	_, rest, _ := strings.Cut(string(data), "\n")
	out := "# " + strings.TrimSpace(dst) + "\n" + rest
	return os.WriteFile(a.profilePath(dst), []byte(out), 0o644)
}

func (a *App) DeleteProfile(name string) error {
	if name == defaultProfile {
		return fmt.Errorf("the default profile cannot be deleted")
	}
	return os.Remove(a.profilePath(name))
}

// GetProfileConfig returns the profile's full effective config as JSON with
// yaml-tag keys (round-trips lists and nested values losslessly for the
// settings tree).
func (a *App) GetProfileConfig(profile string) (string, error) {
	cfg, err := a.loadProfileConfig(profile)
	if err != nil {
		return "", err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	var m map[string]interface{}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return "", err
	}
	out, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// GetConfigValues returns the profile's effective value for each dotted key
// (the settings tree binds to these).
func (a *App) GetConfigValues(profile string, keys []string) (map[string]string, error) {
	cfg, err := a.loadProfileConfig(profile)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, k := range keys {
		v, err := cfg.Get(k)
		if err != nil {
			continue
		}
		out[k] = v
	}
	return out, nil
}

// SetConfigValues applies dotted-path overrides ("key.path" -> YAML value) to
// a profile, validates the result, and saves it.
func (a *App) SetConfigValues(profile string, vals map[string]string) error {
	cfg, err := a.loadProfileConfig(profile)
	if err != nil {
		return err
	}
	for k, v := range vals {
		if err := cfg.Set(k + "=" + v); err != nil {
			return err
		}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	name := profile
	if name == "" {
		name = defaultProfile
	}
	if err := os.MkdirAll(a.profilesDir(), 0o755); err != nil {
		return err
	}
	header := "# " + name + "\n"
	return os.WriteFile(a.profilePath(name), append([]byte(header), data...), 0o644)
}

// GetProfileYAML / SaveProfileYAML power the raw "advanced" editor.
func (a *App) GetProfileYAML(profile string) (string, error) {
	if err := a.ensureDefaultProfile(); err != nil {
		return "", err
	}
	name := profile
	if name == "" {
		name = defaultProfile
	}
	data, err := os.ReadFile(a.profilePath(name))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (a *App) SaveProfileYAML(profile, content string) error {
	body := content
	if i := strings.Index(content, "\n"); i > 0 && strings.HasPrefix(content, "# ") {
		body = content[i+1:]
	}
	if _, err := config.Load([]byte(body)); err != nil {
		return err
	}
	name := profile
	if name == "" {
		name = defaultProfile
	}
	return os.WriteFile(a.profilePath(name), []byte(content), 0o644)
}

// StorageInfo reports the store path and size for the sidebar footer.
type StorageInfo struct {
	Dir    string `json:"dir"`
	SizeMB int    `json:"sizeMB"`
	Crawls int    `json:"crawls"`
}

func (a *App) GetStorageInfo() StorageInfo {
	info := StorageInfo{Dir: a.storeDir}
	var size int64
	_ = filepath.Walk(a.storeDir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			size += fi.Size()
		}
		return nil
	})
	info.SizeMB = int(size / (1024 * 1024))
	if infos, err := store.ListCrawls(a.storeDir); err == nil {
		info.Crawls = len(infos)
	}
	return info
}
