package websocket

import (
	"sync"
	"time"
)

// Message types for AssemblyAI
type BeginMessage struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	ExpiresAt time.Time `json:"expires_at"`
}

type TurnMessage struct {
	Type            string  `json:"type"`
	Transcript      string  `json:"transcript"`
	TurnIsFormatted bool    `json:"turn_is_formatted"`
	Start           float64 `json:"start,omitempty"`
	End             float64 `json:"end,omitempty"`
	Text            string  `json:"text,omitempty"`
	Words           []Word  `json:"words,omitempty"`
}

type Word struct {
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence,omitempty"`
}

type TerminationMessage struct {
	Type                   string  `json:"type"`
	AudioDurationSeconds   float64 `json:"audio_duration_seconds"`
	SessionDurationSeconds float64 `json:"session_duration_seconds"`
}

type TerminateMessage struct {
	Type string `json:"type"`
}

// Transcript represents a completed transcription segment
type Transcript struct {
	Text      string    `json:"text"`
	Start     float64   `json:"start"`
	End       float64   `json:"end"`
	Timestamp time.Time `json:"timestamp"`
}

// SessionContext maintains persistent state for a connection
type SessionContext struct {
	ID                 string       `json:"id"`
	StartTime          time.Time    `json:"start_time"`
	EndTime            *time.Time   `json:"end_time,omitempty"`
	TotalAudioDuration float64      `json:"total_audio_duration"`
	Transcripts        []Transcript `json:"transcripts"`
	Status             string       `json:"status"` // "active", "completed", "error"
	ErrorMessage       string       `json:"error_message,omitempty"`
	Mutex              sync.RWMutex `json:"-"`
}

// Global session store
var (
	sessionStore = make(map[string]*SessionContext)
	storeMutex   = sync.RWMutex{}
)
