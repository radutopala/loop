package config

// DictationLanguage is a language whisper transcribes: its code, as
// dictation.language takes it, and its name.
type DictationLanguage struct {
	Code string
	Name string
}

// DictationLanguages returns the languages whisper knows, by name.
func DictationLanguages() []DictationLanguage {
	return []DictationLanguage{
		{"af", "Afrikaans"},
		{"sq", "Albanian"},
		{"am", "Amharic"},
		{"ar", "Arabic"},
		{"hy", "Armenian"},
		{"as", "Assamese"},
		{"az", "Azerbaijani"},
		{"ba", "Bashkir"},
		{"eu", "Basque"},
		{"be", "Belarusian"},
		{"bn", "Bengali"},
		{"bs", "Bosnian"},
		{"br", "Breton"},
		{"bg", "Bulgarian"},
		{"yue", "Cantonese"},
		{"ca", "Catalan"},
		{"zh", "Chinese"},
		{"hr", "Croatian"},
		{"cs", "Czech"},
		{"da", "Danish"},
		{"nl", "Dutch"},
		{"en", "English"},
		{"et", "Estonian"},
		{"fo", "Faroese"},
		{"fi", "Finnish"},
		{"fr", "French"},
		{"gl", "Galician"},
		{"ka", "Georgian"},
		{"de", "German"},
		{"el", "Greek"},
		{"gu", "Gujarati"},
		{"ht", "Haitian Creole"},
		{"ha", "Hausa"},
		{"haw", "Hawaiian"},
		{"he", "Hebrew"},
		{"hi", "Hindi"},
		{"hu", "Hungarian"},
		{"is", "Icelandic"},
		{"id", "Indonesian"},
		{"it", "Italian"},
		{"ja", "Japanese"},
		{"jw", "Javanese"},
		{"kn", "Kannada"},
		{"kk", "Kazakh"},
		{"km", "Khmer"},
		{"ko", "Korean"},
		{"lo", "Lao"},
		{"la", "Latin"},
		{"lv", "Latvian"},
		{"ln", "Lingala"},
		{"lt", "Lithuanian"},
		{"lb", "Luxembourgish"},
		{"mk", "Macedonian"},
		{"mg", "Malagasy"},
		{"ms", "Malay"},
		{"ml", "Malayalam"},
		{"mt", "Maltese"},
		{"mi", "Maori"},
		{"mr", "Marathi"},
		{"mn", "Mongolian"},
		{"my", "Myanmar"},
		{"ne", "Nepali"},
		{"no", "Norwegian"},
		{"nn", "Nynorsk"},
		{"oc", "Occitan"},
		{"ps", "Pashto"},
		{"fa", "Persian"},
		{"pl", "Polish"},
		{"pt", "Portuguese"},
		{"pa", "Punjabi"},
		{"ro", "Romanian"},
		{"ru", "Russian"},
		{"sa", "Sanskrit"},
		{"sr", "Serbian"},
		{"sn", "Shona"},
		{"sd", "Sindhi"},
		{"si", "Sinhala"},
		{"sk", "Slovak"},
		{"sl", "Slovenian"},
		{"so", "Somali"},
		{"es", "Spanish"},
		{"su", "Sundanese"},
		{"sw", "Swahili"},
		{"sv", "Swedish"},
		{"tl", "Tagalog"},
		{"tg", "Tajik"},
		{"ta", "Tamil"},
		{"tt", "Tatar"},
		{"te", "Telugu"},
		{"th", "Thai"},
		{"bo", "Tibetan"},
		{"tr", "Turkish"},
		{"tk", "Turkmen"},
		{"uk", "Ukrainian"},
		{"ur", "Urdu"},
		{"uz", "Uzbek"},
		{"vi", "Vietnamese"},
		{"cy", "Welsh"},
		{"yi", "Yiddish"},
		{"yo", "Yoruba"},
	}
}

// dictationLanguageEnum is auto, then every language's code.
func dictationLanguageEnum() ([]any, map[string]string) {
	enum := []any{DefaultDictationLanguage}
	labels := map[string]string{DefaultDictationLanguage: "Detect automatically"}
	for _, l := range DictationLanguages() {
		enum = append(enum, l.Code)
		labels[l.Code] = l.Name + " (" + l.Code + ")"
	}
	return enum, labels
}
