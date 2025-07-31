package main

import (
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
	"github.com/raihanakbr/assemblyai-realtime-transcription/internal/websocket"
)

func main() {
	// Load environment variables from .env if present
	if err := godotenv.Load(); err != nil {
		log.Print("No .env file found; using system environment variables")
	}

	// Validate required environment variables
	if os.Getenv(websocket.APIKeyEnvVar) == "" {
		log.Fatalf("Missing environment variable %s", websocket.APIKeyEnvVar)
	}

	// Register HTTP and WebSocket handlers on a dedicated mux
	mux := http.NewServeMux()
	// WebSocket endpoint (using /ws to avoid conflicts)
	mux.HandleFunc("/ws", websocket.HandleWebSocketConnection)
	// REST API endpoints
	mux.HandleFunc("/api/session", websocket.GetSessionHandler)
	mux.HandleFunc("/api/transcripts", websocket.GetTranscriptsHandler)

	// Start the server
	log.Printf("Starting server on %s", websocket.ServerPort)
	log.Printf("WebSocket endpoint: ws://localhost%s/ws?connection_id=<id>", websocket.ServerPort)

	if err := http.ListenAndServe(websocket.ServerPort, mux); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
