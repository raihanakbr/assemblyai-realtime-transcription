package websocket

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

// HandleWebSocketConnection handles a new WebSocket connection from a client
func HandleWebSocketConnection(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	clientWS, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}

	// Get connection ID from query parameters
	connectionID := r.URL.Query().Get("connection_id")

	// Create client connection with default dialer
	client := NewClientConnection(clientWS, connectionID, websocket.DefaultDialer)
	log.Printf("New client connected: %s", client.ID)

	// Connect to AssemblyAI
	if err := client.ConnectToAssemblyAI(); err != nil {
		log.Printf("Failed to connect to AssemblyAI for client %s: %v", client.ID, err)
		client.Close()
		return
	}

	// Handle messages from client
	go func() {
		defer client.Close()

		for {
			messageType, data, err := clientWS.ReadMessage()
			if err != nil {
				log.Printf("Error reading from client %s: %v", client.ID, err)
				break
			}

			switch messageType {
			case websocket.BinaryMessage:
				// Forward audio data to AssemblyAI
				if err := client.HandleAudioData(data); err != nil {
					log.Printf("Error handling audio data for client %s: %v", client.ID, err)
				}
			default:
				log.Printf("Received unknown message type from client %s: %d", client.ID, messageType)
			}
		}
	}()
}

// API endpoint handlers

// GetSessionHandler retrieves session information and transcripts
func GetSessionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	connectionID := r.URL.Query().Get("connection_id")
	if connectionID == "" {
		http.Error(w, "connection_id parameter is required", http.StatusBadRequest)
		return
	}

	storeMutex.RLock()
	session, exists := sessionStore[connectionID]
	storeMutex.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	session.Mutex.RLock()
	transcriptsCopy := make([]Transcript, len(session.Transcripts))
	copy(transcriptsCopy, session.Transcripts)
	session.Mutex.RUnlock()

	// Create a response struct that omits the Mutex
	response := struct {
		ID          string       `json:"id"`
		Transcripts []Transcript `json:"transcripts"`
	}{
		ID:          session.ID,
		Transcripts: transcriptsCopy,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetTranscriptsHandler retrieves only the transcripts for a session
func GetTranscriptsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	connectionID := r.URL.Query().Get("connection_id")
	if connectionID == "" {
		http.Error(w, "connection_id parameter is required", http.StatusBadRequest)
		return
	}

	storeMutex.RLock()
	session, exists := sessionStore[connectionID]
	storeMutex.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	session.Mutex.RLock()
	transcriptsCopy := make([]Transcript, len(session.Transcripts))
	copy(transcriptsCopy, session.Transcripts)
	session.Mutex.RUnlock()

	response := map[string]interface{}{
		"connection_id": connectionID,
		"transcripts":   transcriptsCopy,
		"count":         len(transcriptsCopy),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
