package websocket

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/oklog/ulid/v2"
)

type WebsocketDialer interface {
	Dial(string, http.Header) (*websocket.Conn, *http.Response, error)
}

// ClientConnection represents a client connected to our server
type ClientConnection struct {
	ID       string
	ClientWS *websocket.Conn
	// Dialer used to establish connections to AssemblyAI
	Dialer       WebsocketDialer
	AssemblyWS   *websocket.Conn
	Mutex        sync.Mutex
	Done         chan bool
	AudioBuffer  []byte
	LastSentTime time.Time
	StartTime    time.Time       // Track when audio streaming started
	TotalAudioMs float64         // Track total audio duration processed
	SessionCtx   *SessionContext // Reference to persistent session context
}

// NewClientConnection creates a new client connection with injected WebsocketDialer
func NewClientConnection(clientWS *websocket.Conn, connectionID string, dialer WebsocketDialer) *ClientConnection {
	if connectionID == "" {
		connectionID = ulid.Make().String()
	}

	// Get or create session context
	sessionCtx := getOrCreateSessionContext(connectionID)

	return &ClientConnection{
		ID:           connectionID,
		ClientWS:     clientWS,
		Dialer:       dialer,
		Done:         make(chan bool),
		AudioBuffer:  make([]byte, 0, MaxChunkSize*2), // Pre-allocate buffer
		LastSentTime: time.Now(),
		StartTime:    time.Now(),
		TotalAudioMs: 0.0,
		SessionCtx:   sessionCtx,
	}
}

// getOrCreateSessionContext retrieves existing session or creates new one
func getOrCreateSessionContext(connectionID string) *SessionContext {
	storeMutex.Lock()
	defer storeMutex.Unlock()

	if session, exists := sessionStore[connectionID]; exists {
		// Resume existing session
		session.Mutex.Lock()
		if session.Status == "completed" || session.Status == "error" {
			// Reset session for new connection
			session.Status = "active"
			session.EndTime = nil
			session.ErrorMessage = ""
			session.TotalAudioDuration = 0
			// Keep existing transcripts for historical reference
		}
		session.Mutex.Unlock()
		return session
	}

	// Create new session
	session := &SessionContext{
		ID:          connectionID,
		StartTime:   time.Now(),
		Status:      "active",
		Transcripts: make([]Transcript, 0),
	}
	sessionStore[connectionID] = session

	log.Printf("Created new session context for connection %s", connectionID)
	return session
}

// ConnectToAssemblyAI establishes connection to AssemblyAI
func (cc *ClientConnection) ConnectToAssemblyAI() error {
	// Build AssemblyAI URL with parameters
	u, err := url.Parse(AssemblyAIURL)
	if err != nil {
		return fmt.Errorf("failed to parse AssemblyAI URL: %v", err)
	}

	q := u.Query()
	q.Set("sample_rate", fmt.Sprintf("%d", SampleRate))
	q.Set("format_turns", fmt.Sprintf("%t", FormatTurns))
	u.RawQuery = q.Encode()

	log.Printf("Connecting to AssemblyAI with URL: %s", u.String())

	// Set up headers using environment variable
	apiKey := os.Getenv(APIKeyEnvVar)
	if apiKey == "" {
		return fmt.Errorf("missing environment variable %s", APIKeyEnvVar)
	}
	headers := http.Header{}
	headers.Set("Authorization", apiKey)

	// Connect to AssemblyAI
	assemblyWS, _, err := cc.Dialer.Dial(u.String(), headers)
	if err != nil {
		return fmt.Errorf("failed to connect to AssemblyAI: %v", err)
	}

	cc.AssemblyWS = assemblyWS
	log.Printf("Connected to AssemblyAI for client %s", cc.ID)

	// Start listening to AssemblyAI responses
	go cc.listenToAssemblyAI()

	return nil
}

// listenToAssemblyAI handles messages from AssemblyAI and forwards them to the client
func (cc *ClientConnection) listenToAssemblyAI() {
	defer func() {
		if cc.AssemblyWS != nil {
			cc.AssemblyWS.Close()
		}
	}()

	for {
		select {
		case <-cc.Done:
			return
		default:
			_, message, err := cc.AssemblyWS.ReadMessage()
			if err != nil {
				log.Printf("Error reading from AssemblyAI for client %s: %v", cc.ID, err)
				return
			}

			// Parse the message to determine type
			var baseMsg map[string]interface{}
			if err := json.Unmarshal(message, &baseMsg); err != nil {
				log.Printf("Error parsing AssemblyAI message for client %s: %v", cc.ID, err)
				continue
			}

			msgType, ok := baseMsg["type"].(string)
			if !ok {
				log.Printf("Unknown message type from AssemblyAI for client %s: %v", cc.ID, baseMsg)
				continue
			}

			log.Printf("Received from AssemblyAI (client %s): %s", cc.ID, msgType)

			// Handle different message types
			switch msgType {
			case "Begin":
				var beginMsg BeginMessage
				if err := json.Unmarshal(message, &beginMsg); err == nil {
					log.Printf("Session began for client %s: ID=%s, ExpiresAt=%v", cc.ID, beginMsg.ID, beginMsg.ExpiresAt)
				}
			case "Turn":
				var turnMsg TurnMessage
				if err := json.Unmarshal(message, &turnMsg); err == nil {
					// Only send response when turn is formatted
					if turnMsg.TurnIsFormatted {
						// Extract start and end times from words array
						var startTime, endTime float64
						if len(turnMsg.Words) > 0 {
							startTime = turnMsg.Words[0].Start / 1000.0                // Convert ms to seconds
							endTime = turnMsg.Words[len(turnMsg.Words)-1].End / 1000.0 // Convert ms to seconds
						} else {
							// Fallback: create estimated timestamps based on total audio processed
							currentTimeMs := cc.TotalAudioMs
							estimatedDuration := 2.0 // Assume ~2 seconds for a typical turn
							startTime = (currentTimeMs - estimatedDuration*1000) / 1000.0
							if startTime < 0 {
								startTime = 0.0
							}
							endTime = currentTimeMs / 1000.0
						}

						// Create a simplified response for the client
						response := map[string]interface{}{
							"text":  turnMsg.Transcript,
							"start": startTime,
							"end":   endTime,
						}

						// Store transcript in session context
						transcript := Transcript{
							Text:      turnMsg.Transcript,
							Start:     startTime,
							End:       endTime,
							Timestamp: time.Now(),
						}
						cc.SessionCtx.Mutex.Lock()
						cc.SessionCtx.Transcripts = append(cc.SessionCtx.Transcripts, transcript)
						cc.SessionCtx.Mutex.Unlock()

						// Send to client
						cc.Mutex.Lock()
						if cc.ClientWS != nil {
							if err := cc.ClientWS.WriteJSON(response); err != nil {
								log.Printf("Error sending to client %s: %v", cc.ID, err)
							}
						}
						cc.Mutex.Unlock()

						if len(turnMsg.Words) > 0 {
							log.Printf("Formatted transcript for client %s (%.2fs-%.2fs): %s",
								cc.ID, startTime, endTime, turnMsg.Transcript)
						} else {
							log.Printf("Formatted transcript for client %s (estimated %.2fs-%.2fs): %s",
								cc.ID, startTime, endTime, turnMsg.Transcript)
						}
					} else {
						// Log partial/unformatted transcripts but don't send to client
						log.Printf("Partial transcript for client %s: %s", cc.ID, turnMsg.Transcript)
					}
				}
			case "Termination":
				var termMsg TerminationMessage
				if err := json.Unmarshal(message, &termMsg); err == nil {
					log.Printf("Session terminated for client %s: Audio Duration=%fs, Session Duration=%fs",
						cc.ID, termMsg.AudioDurationSeconds, termMsg.SessionDurationSeconds)

					// Update session context
					cc.SessionCtx.Mutex.Lock()
					cc.SessionCtx.TotalAudioDuration = termMsg.AudioDurationSeconds
					cc.SessionCtx.Status = "completed"
					endTime := time.Now()
					cc.SessionCtx.EndTime = &endTime
					cc.SessionCtx.Mutex.Unlock()
				}
				return
			case "Error":
				// Handle error messages from AssemblyAI
				var errorMsg map[string]interface{}
				if err := json.Unmarshal(message, &errorMsg); err == nil {
					errorCode, _ := errorMsg["error_code"]
					errorMessage, _ := errorMsg["error_message"]
					log.Printf("AssemblyAI Error for client %s: Code=%v, Message=%v", cc.ID, errorCode, errorMessage)

					// Update session context with error
					cc.SessionCtx.Mutex.Lock()
					cc.SessionCtx.Status = "error"
					cc.SessionCtx.ErrorMessage = fmt.Sprintf("Code=%v, Message=%v", errorCode, errorMessage)
					endTime := time.Now()
					cc.SessionCtx.EndTime = &endTime
					cc.SessionCtx.Mutex.Unlock()

					// Optionally send error to client
					response := map[string]interface{}{
						"error":   true,
						"message": fmt.Sprintf("Transcription error: %v", errorMessage),
					}

					cc.Mutex.Lock()
					if cc.ClientWS != nil {
						cc.ClientWS.WriteJSON(response)
					}
					cc.Mutex.Unlock()
				} else {
					log.Printf("Failed to parse error message for client %s: %v", cc.ID, err)
				}
				return
			}
		}
	}
}

// HandleAudioData buffers and forwards audio data to AssemblyAI in appropriate chunks
func (cc *ClientConnection) HandleAudioData(audioData []byte) error {
	if cc.AssemblyWS == nil {
		return fmt.Errorf("AssemblyAI connection not established")
	}

	cc.Mutex.Lock()
	defer cc.Mutex.Unlock()

	// Add new audio data to buffer
	cc.AudioBuffer = append(cc.AudioBuffer, audioData...)

	// Process buffer and send chunks if we have enough data
	for len(cc.AudioBuffer) >= MinChunkSize {
		// Determine chunk size (aim for MaxChunkSize but respect timing)
		chunkSize := MaxChunkSize
		if len(cc.AudioBuffer) < MaxChunkSize {
			chunkSize = len(cc.AudioBuffer)
		}

		// Ensure we don't split in the middle of a sample (2 bytes for 16-bit)
		chunkSize = (chunkSize / 2) * 2

		// Calculate timing for this chunk
		chunkDurationMs := float64(chunkSize) / float64(BytesPerSecond) * 1000

		// Only send if we meet timing requirements or if we have a lot of buffered data
		timeSinceLastSent := time.Since(cc.LastSentTime)
		shouldSend := chunkDurationMs >= MinChunkDurationMs && chunkDurationMs <= MaxChunkDurationMs

		// Also send if we have too much buffered data (prevent infinite buffering)
		if !shouldSend && len(cc.AudioBuffer) > MaxChunkSize*3 {
			shouldSend = true
			chunkSize = MaxChunkSize
			log.Printf("Force sending chunk due to buffer overflow for client %s", cc.ID)
		}

		// Also send if enough time has passed since last send
		if !shouldSend && timeSinceLastSent > time.Duration(MaxChunkDurationMs)*time.Millisecond {
			shouldSend = true
			if chunkSize > MaxChunkSize {
				chunkSize = MaxChunkSize
			}
		}

		// Also send if we have remaining data that's too small but we haven't received new data for a while
		// Add padding to meet minimum duration requirement
		if !shouldSend && timeSinceLastSent > 500*time.Millisecond && len(cc.AudioBuffer) > 0 {
			// Calculate how much padding we need to reach minimum duration
			minSizeNeeded := MinChunkSize
			if len(cc.AudioBuffer) < minSizeNeeded {
				paddingNeeded := minSizeNeeded - len(cc.AudioBuffer)
				// Add silent audio padding (zeros)
				padding := make([]byte, paddingNeeded)
				cc.AudioBuffer = append(cc.AudioBuffer, padding...)
				chunkSize = minSizeNeeded
				shouldSend = true
				log.Printf("Added %d bytes of padding to meet minimum chunk size for client %s", paddingNeeded, cc.ID)
			}
		}

		if shouldSend {
			// Extract chunk from buffer
			chunk := make([]byte, chunkSize)
			copy(chunk, cc.AudioBuffer[:chunkSize])

			// Send chunk to AssemblyAI
			err := cc.AssemblyWS.WriteMessage(websocket.BinaryMessage, chunk)
			if err != nil {
				return fmt.Errorf("failed to send audio to AssemblyAI: %v", err)
			}

			// Remove sent data from buffer
			cc.AudioBuffer = cc.AudioBuffer[chunkSize:]
			cc.LastSentTime = time.Now()

			// Track total audio duration for timestamp estimation
			cc.TotalAudioMs += chunkDurationMs

			log.Printf("Sent chunk: %d bytes (%.1fms) to AssemblyAI for client %s, buffer remaining: %d bytes",
				chunkSize, chunkDurationMs, cc.ID, len(cc.AudioBuffer))
		} else {
			// Not enough data yet, break and wait for more
			break
		}
	}

	return nil
}

// Close closes the client connection
func (cc *ClientConnection) Close() {
	cc.Mutex.Lock()
	defer cc.Mutex.Unlock()

	// Signal done
	select {
	case <-cc.Done:
		// Already closed
	default:
		close(cc.Done)
	}

	// No need to flush buffer when client disconnects - there's no one to send results to
	if len(cc.AudioBuffer) > 0 {
		log.Printf("Discarding %d bytes of remaining audio data for disconnected client %s", len(cc.AudioBuffer), cc.ID)
		cc.AudioBuffer = cc.AudioBuffer[:0] // Clear buffer
	}

	// Send termination message to AssemblyAI
	if cc.AssemblyWS != nil {
		terminateMsg := TerminateMessage{Type: "Terminate"}
		if data, err := json.Marshal(terminateMsg); err == nil {
			err := cc.AssemblyWS.WriteMessage(websocket.TextMessage, data)
			if err != nil {
				log.Printf("Error sending termination message to AssemblyAI for client %s: %v", cc.ID, err)
			} else {
				log.Printf("Sent termination message to AssemblyAI for client %s", cc.ID)
			}
		}
		time.Sleep(100 * time.Millisecond) // Give time for message to be sent
		cc.AssemblyWS.Close()
		cc.AssemblyWS = nil
	}

	// Close client connection
	if cc.ClientWS != nil {
		cc.ClientWS.Close()
		cc.ClientWS = nil
	}

	// Update session context if connection is ending
	if cc.SessionCtx != nil {
		cc.SessionCtx.Mutex.Lock()
		if cc.SessionCtx.Status == "active" {
			cc.SessionCtx.Status = "completed"
			cc.SessionCtx.TotalAudioDuration = cc.TotalAudioMs / 1000.0
			endTime := time.Now()
			cc.SessionCtx.EndTime = &endTime
		}
		cc.SessionCtx.Mutex.Unlock()
	}

	log.Printf("Closed connection for client %s (processed %.2fs of audio)", cc.ID, cc.TotalAudioMs/1000.0)
}
