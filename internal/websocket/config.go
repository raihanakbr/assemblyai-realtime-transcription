package websocket

// Configuration constants
const (
	// Environment variable name for API key
	APIKeyEnvVar  = "ASSEMBLY_AI_API_KEY"
	AssemblyAIURL = "wss://streaming.assemblyai.com/v3/ws"
	ServerPort    = ":8080"
	SampleRate    = 16000
	FormatTurns   = true

	// Audio chunk configuration
	MaxChunkDurationMs = 1000 // Maximum chunk duration in milliseconds
	MinChunkDurationMs = 50   // Minimum chunk duration in milliseconds
	BytesPerSecond     = SampleRate * 2
	MaxChunkSize       = (MaxChunkDurationMs * BytesPerSecond) / 1000
	MinChunkSize       = (MinChunkDurationMs * BytesPerSecond) / 1000
)
