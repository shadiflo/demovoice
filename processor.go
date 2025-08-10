package main

import (
    "demovoice/decoder"
    "fmt"
    "github.com/go-audio/audio"
    "github.com/go-audio/wav"
    dem "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs"
    "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/msgs2"
    "log"
    "os"
    "path/filepath"
    "runtime"
    "strconv"
    "strings"
    "sync"
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
func ProcessDemo(demoPath string, demoID string) error {
    // Create a map of users to voice data
    voiceDataPerPlayer := map[string][][]byte{}
    playerTeams := map[string]int{} // Track which team each player is on
    var format string
    var mutex sync.RWMutex

    // Open the demo file
    file, err := os.Open(demoPath)
    if err != nil {
        return fmt.Errorf("failed to open demo file: %v", err)
    }
    defer file.Close()

    parser := dem.NewParser(file)

    // We'll get team information after parsing by checking player teams
    // This will be handled in the post-processing phase

    // Add a parser register for the VoiceData net message
    parser.RegisterNetMessageHandler(func(m *msgs2.CSVCMsg_VoiceData) {
        // Early filtering - skip empty voice data
        if len(m.Audio.VoiceData) == 0 {
            return
        }

        // Get the users Steam ID 64
        steamId := strconv.Itoa(int(m.GetXuid()))
        format = m.Audio.Format.String()

        mutex.Lock()
        voiceDataPerPlayer[steamId] = append(voiceDataPerPlayer[steamId], m.Audio.VoiceData)
        mutex.Unlock()
    })

    // Parse the full demo file
    err = parser.ParseToEnd()
    if err != nil {
        return fmt.Errorf("failed to parse demo: %v", err)
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

    if len(filteredPlayers) == 0 {
        log.Printf("No voice data found in demo %s", demoID)
        return nil
    }

    // Process voice data in parallel
    err = processVoiceDataParallel(filteredPlayers, format, demoID)
    if err != nil {
        return err
    }

    // Save team information to metadata
    return saveTeamMetadata(demoID, playerTeams)
}

// processVoiceDataParallel processes multiple players' voice data concurrently
func processVoiceDataParallel(voiceDataPerPlayer map[string][][]byte, format, demoID string) error {
    // Determine optimal number of workers (don't exceed number of CPUs or players)
    numWorkers := runtime.NumCPU()
    if len(voiceDataPerPlayer) < numWorkers {
        numWorkers = len(voiceDataPerPlayer)
    }

    // Create job channel and worker goroutines
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

    if len(processingErrors) > 0 {
        log.Printf("Completed with %d errors out of %d players", len(processingErrors), len(voiceDataPerPlayer))
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

// Keep original function for backward compatibility
func convertAudioDataToWavFiles(payloads [][]byte, fileName string) error {
    return convertAudioDataToWavFilesOptimized(payloads, fileName)
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

// Keep original function for backward compatibility
func opusToWav(data [][]byte, wavName string) error {
    return opusToWavOptimized(data, wavName)
}
