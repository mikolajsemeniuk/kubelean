package heatmap

// Record is one trial: a single model call on one variant of one scenario. raw
// is the source of truth (full response); answer is the parse of it, null on
// failure. For baseline trials Doc/Field are null and Kind/Category are empty.
type Record struct {
	Scenario    string  `json:"scenario"`
	Group       string  `json:"group"`
	FaultClass  string  `json:"fault_class"`
	Variant     string  `json:"variant"`
	Doc         *int    `json:"doc"`
	Kind        string  `json:"kind"`
	Field       *string `json:"field"`
	Category    string  `json:"category"`
	Valid       bool    `json:"valid"`
	Seed        int64   `json:"seed"`
	K           int     `json:"k"`
	Model       string  `json:"model"`
	ModelDigest string  `json:"model_digest"`
	Temp        float64 `json:"temp"`
	NumCtx      int     `json:"num_ctx"`
	Raw         string  `json:"raw"`
	Answer      *string `json:"answer"`
}
