// Package isocodes embeds the ISO 639-1 language and ISO 3166-1 alpha-2
// country registries for hreflang code validation (DESIGN.md §5.6). Only
// assigned codes count: structurally well-formed but unassigned codes
// ("zz", "en-ZZ") and reserved-but-unassigned regions ("UK" — GB is the
// assigned code) are invalid, matching Google's hreflang requirements.
package isocodes

import "strings"

// iso6391 is the complete set of assigned ISO 639-1 two-letter language
// codes (184 codes, current registry).
const iso6391 = `aa ab ae af ak am an ar as av ay az
ba be bg bh bi bm bn bo br bs
ca ce ch co cr cs cu cv cy
da de dv dz
ee el en eo es et eu
fa ff fi fj fo fr fy
ga gd gl gn gu gv
ha he hi ho hr ht hu hy hz
ia id ie ig ii ik io is it iu
ja jv
ka kg ki kj kk kl km kn ko kr ks ku kv kw ky
la lb lg li ln lo lt lu lv
mg mh mi mk ml mn mr ms mt my
na nb nd ne ng nl nn no nr nv ny
oc oj om or os
pa pi pl ps pt
qu
rm rn ro ru rw
sa sc sd se sg si sk sl sm sn so sq sr ss st su sv sw
ta te tg th ti tk tl tn to tr ts tt tw ty
ug uk ur uz
ve vi vo
wa wo
xh
yi yo
za zh zu`

// iso3166 is the complete set of officially assigned ISO 3166-1 alpha-2
// country codes (249 codes, current registry). Exceptionally reserved and
// user-assigned ranges (UK, AA, ZZ, X*) are deliberately absent.
const iso3166 = `ad ae af ag ai al am ao aq ar as at au aw ax az
ba bb bd be bf bg bh bi bj bl bm bn bo bq br bs bt bv bw by bz
ca cc cd cf cg ch ci ck cl cm cn co cr cu cv cw cx cy cz
de dj dk dm do dz
ec ee eg eh er es et
fi fj fk fm fo fr
ga gb gd ge gf gg gh gi gl gm gn gp gq gr gs gt gu gw gy
hk hm hn hr ht hu
id ie il im in io iq ir is it
je jm jo jp
ke kg kh ki km kn kp kr kw ky kz
la lb lc li lk lr ls lt lu lv ly
ma mc md me mf mg mh mk ml mm mn mo mp mq mr ms mt mu mv mw mx my mz
na nc ne nf ng ni nl no np nr nu nz
om
pa pe pf pg ph pk pl pm pn pr ps pt pw py
qa
re ro rs ru rw
sa sb sc sd se sg sh si sj sk sl sm sn so sr ss st sv sx sy sz
tc td tf tg th tj tk tl tm tn to tr tt tv tw tz
ua ug um us uy uz
va vc ve vg vi vn vu
wf ws
ye yt
za zm zw`

func toSet(codes string) map[string]bool {
	set := map[string]bool{}
	for c := range strings.FieldsSeq(codes) {
		set[c] = true
	}
	return set
}

var (
	languages = toSet(iso6391)
	regions   = toSet(iso3166)
)

// ValidLanguage reports whether code is an assigned ISO 639-1 two-letter
// language code (case-insensitive). Full hreflang tags ("en-US") and
// ISO 639-2/3 three-letter codes are not bare languages.
func ValidLanguage(code string) bool {
	return len(code) == 2 && languages[strings.ToLower(code)]
}

// ValidRegion reports whether code is an officially assigned ISO 3166-1
// alpha-2 country code (case-insensitive).
func ValidRegion(code string) bool {
	return len(code) == 2 && regions[strings.ToLower(code)]
}
