package isocodes

import "testing"

func TestValidLanguage(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		// assigned ISO 639-1 codes spanning the alphabet
		{"aa", true},
		{"ar", true},
		{"bn", true},
		{"cs", true},
		{"da", true},
		{"de", true},
		{"el", true},
		{"en", true},
		{"es", true},
		{"fa", true},
		{"fi", true},
		{"fr", true},
		{"he", true},
		{"hi", true},
		{"id", true},
		{"it", true},
		{"ja", true},
		{"ko", true},
		{"nl", true},
		{"no", true},
		{"pl", true},
		{"pt", true},
		{"ru", true},
		{"sv", true},
		{"ta", true},
		{"th", true},
		{"tr", true},
		{"uk", true}, // Ukrainian — a valid language even though UK is not a region
		{"ur", true},
		{"vi", true},
		{"zh", true},
		// case-insensitive
		{"EN", true},
		{"En", true},
		{"dE", true},
		{"ZH", true},
		{"Ja", true},
		// two-letter but not assigned in ISO 639-1
		{"zz", false},
		{"ZZ", false},
		{"xx", false},
		{"qq", false},
		// wrong shape
		{"q", false},
		{"e", false},
		{"abc", false},
		{"eng", false}, // ISO 639-2/3, not 639-1
		{"english", false},
		{"", false},
		{"12", false},
		// full hreflang tags are not bare language codes
		{"EN-us", false},
		{"en-US", false},
	}
	for _, tt := range tests {
		if got := ValidLanguage(tt.code); got != tt.want {
			t.Errorf("ValidLanguage(%q) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestValidRegion(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		// assigned ISO 3166-1 alpha-2 codes
		{"US", true},
		{"GB", true},
		{"DE", true},
		{"FR", true},
		{"IN", true},
		{"CN", true},
		{"JP", true},
		{"BR", true},
		{"AU", true},
		{"CA", true},
		{"ES", true},
		{"IT", true},
		{"NL", true},
		{"RU", true},
		{"ZA", true},
		// case-insensitive
		{"us", true},
		{"gb", true},
		{"In", true},
		{"jP", true},
		{"za", true},
		// not assigned
		{"ZZ", false},
		{"zz", false},
		{"XX", false},
		{"UK", false}, // exceptionally reserved; GB is the assigned code
		{"uk", false},
		{"AA", false}, // user-assigned range, not assigned
		{"aa", false},
		// wrong shape
		{"", false},
		{"USA", false},
		{"U", false},
		{"12", false},
	}
	for _, tt := range tests {
		if got := ValidRegion(tt.code); got != tt.want {
			t.Errorf("ValidRegion(%q) = %v, want %v", tt.code, got, tt.want)
		}
	}
}
