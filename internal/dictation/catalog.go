// Package dictation transcribes speech live, on the host, with whisper.cpp's
// whisper-server: it downloads the model, runs the server through
// localserver, and turns a stream of PCM audio into text that grows as the
// user speaks.
package dictation

// Model is a downloadable whisper.cpp model.
type Model struct {
	Name   string
	Size   int64
	SHA256 string
}

// FileName is the model's file name, in the download and on disk.
func (m Model) FileName() string { return "ggml-" + m.Name + ".bin" }

// modelBaseURL serves the whisper.cpp ggml models.
const modelBaseURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"

// Models lists the models dictation can use.
func Models() []Model {
	return []Model{
		{Name: "large-v3-turbo-q5_0", Size: 574041195, SHA256: "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2"},
		{Name: "large-v3-turbo", Size: 1624555275, SHA256: "1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69"},
	}
}

// LookupModel returns the model named name.
func LookupModel(name string) (Model, bool) {
	for _, m := range Models() {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}
