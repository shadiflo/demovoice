package main

import (
	"bufio"
	"bytes"
	"demovoice/decoder"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/klauspost/compress/zstd"
	dem "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs"
	"github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/events"
	"github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/msgs2"
)

// AudioBufferPool manages reusable audio buffers to reduce GC pressure
type AudioBufferPool struct {
	int32Pool   sync.Pool
	float32Pool sync.Pool
}

var bufferPool = &AudioBufferPool{
	int32Pool: sync.Pool{
		New: func() interface{} {
			return make([]int, 0, 4096)
		},
	},
	float32Pool: sync.Pool{
		New: func() interface{} {
			return make([]float32, 0, 4096)
		},
	},
}

func (p *AudioBufferPool) GetInt32Buffer() []int {
	return p.int32Pool.Get().([]int)[:0]
}

func (p *AudioBufferPool) PutInt32Buffer(buf []int) {
	if cap(buf) <= 8192 { // Prevent memory leaks from oversized buffers
		p.int32Pool.Put(buf)
	}
}

func (p *AudioBufferPool) GetFloat32Buffer() []float32 {
	return p.float32Pool.Get().([]float32)[:0]
}

func (p *AudioBufferPool) PutFloat32Buffer(buf []float32) {
	if cap(buf) <= 8192 {
		p.float32Pool.Put(buf)
	}
}

// VoiceProcessingJob represents a job for processing a player's voice data
type VoiceProcessingJob struct {
	PlayerID   string
	VoiceData  [][]byte
	Format     string
	OutputPath string
}

// ProcessDemo processes a demo file and extracts voice data with optimizations
// The demoID parameter is used to associate voice files with a specific demo
// If chatOnly is true, only chat logs are extracted (much faster, no voice processing)
func ProcessDemo(demoPath string, demoID string, chatOnly bool) (playerTeams map[string]int, err error) {
	// Recover from panics in the parser
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during demo processing: %v", r)
			log.Printf("❌ Recovered from panic in ProcessDemo: %v", r)
		}
	}()

	startTime := time.Now()

	// Pre-allocate maps with expected capacity (10 players typical)
	voiceDataPerPlayer := make(map[string][][]byte, 10)
	playerTeams = make(map[string]int, 10)
	var format string
	var chatLogs []string

	// Use separate mutexes per player to reduce contention
	playerMutexes := make(map[string]*sync.Mutex, 10)
	var mapMutex sync.RWMutex

	// Track voice packets for progress
	var voicePacketCount int64

	// Open the demo file
	file, err := os.Open(demoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open demo file: %v", err)
	}
	defer file.Close()

	// Get file size for progress logging
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat demo file: %v", err)
	}
	fileSizeMB := float64(fileInfo.Size()) / (1024 * 1024)
	log.Printf("Demo file size: %.2f MB", fileSizeMB)

	// Load entire file into memory for fastest parsing
	log.Printf("Loading demo into memory...")
	loadStart := time.Now()
	fileData, err := io.ReadAll(bufio.NewReaderSize(file, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read demo file: %v", err)
	}
	if len(fileData) < 100 {
		return nil, fmt.Errorf("demo file too small or empty: %d bytes", len(fileData))
	}
	log.Printf("Loaded %.2f MB into memory in %.2fs", fileSizeMB, time.Since(loadStart).Seconds())

	var demoReader io.Reader = bytes.NewReader(fileData)

	// Check if file is zstd compressed (.dem.zst) and decompress if needed
	if strings.HasSuffix(strings.ToLower(demoPath), ".zst") {
		log.Printf("Decompressing zstd demo...")
		decompressStart := time.Now()

		// Create decoder with max concurrency from the in-memory data
		zstdDecoder, err := zstd.NewReader(bytes.NewReader(fileData),
			zstd.WithDecoderConcurrency(runtime.NumCPU()),
			zstd.WithDecoderLowmem(false),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd decoder: %v", err)
		}

		// Decompress entire file
		decompressedData, err := io.ReadAll(zstdDecoder)
		zstdDecoder.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to decompress: %v", err)
		}

		decompressedMB := float64(len(decompressedData)) / (1024 * 1024)
		log.Printf("Decompressed %.2f MB -> %.2f MB in %.2fs (%.1fx ratio)",
			fileSizeMB, decompressedMB, time.Since(decompressStart).Seconds(),
			decompressedMB/fileSizeMB)

		demoReader = bytes.NewReader(decompressedData)
		fileSizeMB = decompressedMB
	}

	// Use optimized parser config for faster parsing
	parserConfig := dem.DefaultParserConfig
	parserConfig.MsgQueueBufferSize = 128000         // Reasonable buffer size
	parserConfig.DisableMimicSource1Events = true    // Skip Source 1 event mimicking for CS2

	parser := dem.NewParserWithConfig(demoReader, parserConfig)

	// Progress logging goroutine
	stopProgress := make(chan bool)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				progress := parser.Progress()
				log.Printf("⏳ Parsing progress: %.1f%% (tick %d)", progress*100, parser.CurrentFrame())
			case <-stopProgress:
				return
			}
		}
	}()

	// Register chat message handler
	parser.RegisterEventHandler(func(e events.ChatMessage) {
		senderName := "Console"
		if e.Sender != nil {
			senderName = e.Sender.Name
		}

		// Note: FACEIT demos only contain all chat, not team chat
		chatLogs = append(chatLogs, fmt.Sprintf("[%s] %s: %s", parser.CurrentTime().String(), senderName, e.Text))
	})



	// Only register voice handler if not chat-only mode
	if !chatOnly {
		// Optimize parser - only register voice data handler
		// Skip other events to reduce parsing overhead
		parser.RegisterNetMessageHandler(func(m *msgs2.CSVCMsg_VoiceData) {
			// Early filtering - skip empty voice data
			if len(m.Audio.VoiceData) == 0 {
				return
			}

			// Track packet count for progress
			atomic.AddInt64(&voicePacketCount, 1)

			// Get the users Steam ID 64
			steamId := strconv.Itoa(int(m.GetXuid()))
			format = m.Audio.Format.String()

			// Get or create player-specific mutex
			mapMutex.RLock()
			playerMutex, exists := playerMutexes[steamId]
			mapMutex.RUnlock()

			if !exists {
				mapMutex.Lock()
				if _, exists = playerMutexes[steamId]; !exists {
					playerMutexes[steamId] = &sync.Mutex{}
					voiceDataPerPlayer[steamId] = make([][]byte, 0, 256) // Pre-allocate expected voice packets
				}
				playerMutex = playerMutexes[steamId]
				mapMutex.Unlock()
			}

			// Lock only this player's data
			playerMutex.Lock()
			voiceDataPerPlayer[steamId] = append(voiceDataPerPlayer[steamId], m.Audio.VoiceData)
			playerMutex.Unlock()
		})
	}

	// Parse the full demo file
	log.Printf("Starting demo parse for %s (%.2f MB)...", demoID, fileSizeMB)
	err = parser.ParseToEnd()
	close(stopProgress) // Stop progress logging
	parseTime := time.Since(startTime)
	if err != nil {
		return nil, fmt.Errorf("failed to parse demo: %v", err)
	}
	log.Printf("Demo parsing completed for %s in %.2fs (%.2f MB/s, %d voice packets)",
		demoID, parseTime.Seconds(), fileSizeMB/parseTime.Seconds(), atomic.LoadInt64(&voicePacketCount))

	// Capture team info from parser state after parsing
	for _, player := range parser.GameState().Participants().All() {
		playerTeams[strconv.FormatUint(player.SteamID64, 10)] = int(player.Team)
	}

	// Clean old files from the same demo if they exist (when re-processing the same demo)
	cleanupOldDemoFiles(demoID)

	// Filter out players with no voice data
	filteredPlayers := make(map[string][][]byte)
	for playerId, voiceData := range voiceDataPerPlayer {
		if len(voiceData) > 0 {
			filteredPlayers[playerId] = voiceData
		}
	}

	// Save chat logs
	if len(chatLogs) > 0 {
		chatLogPath := filepath.Join(outputDir, demoID+"_chat.txt")
		f, err := os.Create(chatLogPath)
		if err == nil {
			defer f.Close()
			for _, line := range chatLogs {
				f.WriteString(line + "\n")
			}
			log.Printf("Saved %d chat messages to %s", len(chatLogs), chatLogPath)
		} else {
			log.Printf("Failed to save chat logs: %v", err)
		}
	}

	// If chat-only mode, skip voice processing entirely
	if chatOnly {
		log.Printf("Chat-only mode: Skipping voice processing for demo %s", demoID)
		return playerTeams, nil
	}

	if len(filteredPlayers) == 0 {
		log.Printf("No voice data found in demo %s", demoID)
		return playerTeams, nil
	}

	// Process voice data in parallel
	err = processVoiceDataParallel(filteredPlayers, format, demoID)
	if err != nil {
		return nil, err
	}

	return playerTeams, nil
}

// processVoiceDataParallel processes multiple players' voice data concurrently
func processVoiceDataParallel(voiceDataPerPlayer map[string][][]byte, format, demoID string) error {
	startTime := time.Now()

	// Use all available CPU cores for maximum throughput
	// On 6-core ARM server, this will use all 6 cores
	numWorkers := runtime.NumCPU()

	// Only reduce workers if we have very few players
	if len(voiceDataPerPlayer) < 3 && len(voiceDataPerPlayer) > 0 {
		numWorkers = len(voiceDataPerPlayer)
	}

	// Calculate total voice data size
	var totalPackets int
	for _, packets := range voiceDataPerPlayer {
		totalPackets += len(packets)
	}

	log.Printf("Processing %d players (%d packets) with %d workers on %d CPUs",
		len(voiceDataPerPlayer), totalPackets, numWorkers, runtime.NumCPU())

	// Create buffered channels for better throughput
	jobs := make(chan VoiceProcessingJob, len(voiceDataPerPlayer))
	errors := make(chan error, len(voiceDataPerPlayer))
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				var err error
				if job.Format == "VOICEDATA_FORMAT_OPUS" {
					err = opusToWavOptimized(job.VoiceData, job.OutputPath)
				} else if job.Format == "VOICEDATA_FORMAT_STEAM" {
					err = convertAudioDataToWavFilesOptimized(job.VoiceData, job.OutputPath)
				}

				if err != nil {
					log.Printf("Error processing %s format data for player %s: %v", job.Format, job.PlayerID, err)
					errors <- fmt.Errorf("player %s: %v", job.PlayerID, err)
				} else {
					errors <- nil
				}
			}
		}()
	}

	// Send jobs to workers
	for playerId, voiceData := range voiceDataPerPlayer {
		jobs <- VoiceProcessingJob{
			PlayerID:   playerId,
			VoiceData:  voiceData,
			Format:     format,
			OutputPath: filepath.Join(outputDir, fmt.Sprintf("%s_%s.wav", playerId, demoID)),
		}
	}
	close(jobs)

	// Wait for all workers to complete
	wg.Wait()
	close(errors)

	// Collect any errors
	var processingErrors []error
	for err := range errors {
		if err != nil {
			processingErrors = append(processingErrors, err)
		}
	}

	processTime := time.Since(startTime)
	if len(processingErrors) > 0 {
		log.Printf("Voice processing completed in %.2fs with %d errors out of %d players",
			processTime.Seconds(), len(processingErrors), len(voiceDataPerPlayer))
	} else {
		log.Printf("Voice processing completed in %.2fs for %d players (%d packets)",
			processTime.Seconds(), len(voiceDataPerPlayer), totalPackets)
	}

	return nil
}

// saveTeamMetadata updates the metadata with team information from the demo
func saveTeamMetadata(demoID string, playerTeams map[string]int) error {
	metadata, err := metadataStore.LoadMetadata(demoID)
	if err != nil {
		log.Printf("Warning: Could not load metadata to update teams: %v", err)
		return nil // Don't fail the whole process
	}

	// Update players with team information from demo
	// Team 2 = Terrorists, Team 3 = Counter-Terrorists in CS2
	for i := range metadata.Players {
		player := &metadata.Players[i]
		if teamNum, exists := playerTeams[player.SteamID]; exists {
			switch teamNum {
			case 2:
				player.Team = "Team 1" // Terrorists
			case 3:
				player.Team = "Team 2" // Counter-Terrorists
			default:
				player.Team = "" // Unassigned/Spectator
			}
		}
	}

	// Save updated metadata back
	return metadataStore.UpdateMetadata(metadata)
}

// Helper function to clean up old files related to the same demo
func cleanupOldDemoFiles(demoID string) {
	// Delete the old metadata file if it exists
	metadataPath := filepath.Join(outputDir, demoID+".json")
	os.Remove(metadataPath)

	// Delete old WAV files from this demo
	files, err := os.ReadDir(outputDir)
	if err != nil {
		log.Printf("Error reading output directory: %v", err)
		return
	}

	for _, file := range files {
		if !file.IsDir() && strings.Contains(file.Name(), "_"+demoID+".wav") {
			os.Remove(filepath.Join(outputDir, file.Name()))
		}
	}
	
	// Delete chat logs
	os.Remove(filepath.Join(outputDir, demoID+"_chat.txt"))

	log.Printf("Cleaned up old files for demo ID: %s", demoID)
}

// convertAudioDataToWavFilesOptimized processes STEAM format with optimizations
func convertAudioDataToWavFilesOptimized(payloads [][]byte, fileName string) error {
	// This sample rate can be set using data from the VoiceData net message.
	// But every demo processed has used 24000 and is single channel.
	voiceDecoder, err := decoder.NewOpusDecoder(24000, 1)
	if err != nil {
		return fmt.Errorf("failed to create decoder: %v", err)
	}

	// Use buffer pool for better memory management
	o := bufferPool.GetInt32Buffer()
	defer bufferPool.PutInt32Buffer(o)

	// Pre-allocate with estimated capacity
	if cap(o) < len(payloads)*480 {
		o = make([]int, 0, len(payloads)*480)
	}

	for _, payload := range payloads {
		c, err := decoder.DecodeChunk(payload)
		if err != nil {
			log.Printf("Error decoding chunk: %v", err)
			continue
		}

		// Not silent frame
		if c != nil && len(c.Data) > 0 {
			pcm, err := voiceDecoder.Decode(c.Data)
			if err != nil {
				log.Printf("Error decoding PCM: %v", err)
				continue
			}

			// Convert in-place to avoid allocation
			startLen := len(o)
			o = o[:startLen+len(pcm)]
			for i, v := range pcm {
				// Float32 buffer implementation is wrong in go-audio, so we have to convert to int before encoding
				o[startLen+i] = int(v * 2147483647)
			}
		}
	}

	// Create output file directly (WAV encoder needs WriteSeeker)
	outFile, err := os.Create(fileName)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer outFile.Close()

	// Encode new wav file, from decoded opus data.
	enc := wav.NewEncoder(outFile, 24000, 32, 1, 1)
	defer enc.Close()

	buf := &audio.IntBuffer{
		Data: o,
		Format: &audio.Format{
			SampleRate:  24000,
			NumChannels: 1,
		},
	}

	// Write voice data to the file.
	if err := enc.Write(buf); err != nil {
		return fmt.Errorf("failed to write WAV data: %v", err)
	}

	return nil
}

// opusToWavOptimized processes OPUS format with optimizations
func opusToWavOptimized(data [][]byte, wavName string) error {
	opusDecoder, err := decoder.NewDecoder(48000, 1)
	if err != nil {
		return fmt.Errorf("failed to create opus decoder: %v", err)
	}

	// Use buffer pool for better memory management
	pcmBuffer := bufferPool.GetInt32Buffer()
	defer bufferPool.PutInt32Buffer(pcmBuffer)

	// Pre-allocate with estimated capacity
	if cap(pcmBuffer) < len(data)*1024 {
		pcmBuffer = make([]int, 0, len(data)*1024)
	}

	for _, d := range data {
		pcm, err := decoder.Decode(opusDecoder, d)
		if err != nil {
			log.Printf("Error decoding opus data: %v", err)
			continue
		}

		// Convert in-place to avoid allocation
		startLen := len(pcmBuffer)
		pcmBuffer = pcmBuffer[:startLen+len(pcm)]
		for i, p := range pcm {
			pcmBuffer[startLen+i] = int(p * 2147483647)
		}
	}

	// Create output file directly (WAV encoder needs WriteSeeker)
	file, err := os.Create(wavName)
	if err != nil {
		return fmt.Errorf("failed to create WAV file: %v", err)
	}
	defer file.Close()

	enc := wav.NewEncoder(file, 48000, 32, 1, 1)
	defer enc.Close()

	buffer := &audio.IntBuffer{
		Data: pcmBuffer,
		Format: &audio.Format{
			SampleRate:  48000,
			NumChannels: 1,
		},
	}

	err = enc.Write(buffer)
	if err != nil {
		return fmt.Errorf("failed to write WAV data: %v", err)
	}

	return nil
}

