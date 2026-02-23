package vdclient

// SpeakRequest is the request body for POST /speak.
type SpeakRequest struct {
	Text    string `json:"text"`
	Backend string `json:"backend,omitempty"`
	Model   string `json:"model,omitempty"`
	Voice   string `json:"voice,omitempty"`
	NoLog   bool   `json:"nolog,omitempty"`
}

// SpeakResponse is the response from POST /speak.
type SpeakResponse struct {
	Status     string `json:"status"`
	QueueDepth int    `json:"queue_depth"`
	Backend    string `json:"backend"`
}

// HealthResponse is the response from GET /health.
type HealthResponse struct {
	Status       string `json:"status"`
	QueueDepth   int    `json:"queue_depth"`
	SpeachesURL  string `json:"speaches_url"`
	PocketTTSURL string `json:"pocket_tts_url"`
	STTURL       string `json:"stt_url"`
	STTSocket    string `json:"stt_socket"`
}

// StopResponse is the response from POST /stop.
type StopResponse struct {
	Status string `json:"status"`
}

// ErrorResponse is returned on HTTP errors from the daemon.
type ErrorResponse struct {
	Error string `json:"error"`
}
