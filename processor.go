package main

import (
    "CS2VoiceData/decoder"
    "fmt"
    "github.com/go-audio/audio"
    "github.com/go-audio/wav"
    dem "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs"
    "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/msgs2"
    "log"
    "os"
    "path/filepath"
    "strconv"
    "strings"
)

// Modified to accept a demoID parameter
func ProcessDemo(demoPath string, demoID string) error {
    // Create a map of users to voice data
    voiceDataPerPlayer := map[string][][]byte{}

    // Open the demo file
    file, err := os.Open(demoPath)
    if err != nil {
        return fmt.Errorf("failed to open demo file: %v", err)
    }
    defer file.Close()

    parser := dem.NewParser(file)
    var format string

    // Add a parser register for the VoiceData net message
    parser.RegisterNetMessageHandler(func(m *msgs2.CSVCMsg_VoiceData) {
        // Get the users Steam ID 64
        steamId := strconv.Itoa(int(m.GetXuid()))
        // Append voice data to map
        format = m.Audio.Format.String()
        voiceDataPerPlayer[steamId] = append(voiceDataPerPlayer[steamId], m.Audio.VoiceData)
    })

    // Parse the full demo file
    err = parser.ParseToEnd()
    if err != nil {
        return fmt.Errorf("failed to parse demo: %v", err)
    }

    // Clean old files from the same demo if they exist (when re-processing the same demo)
    cleanupOldDemoFiles(demoID)

    // For each user's data, create a wav file containing their voice comms
    for playerId, voiceData := range voiceDataPerPlayer {
        // Add demoID to filename so we can associate voices with specific demos
        wavFilePath := filepath.Join(outputDir, fmt.Sprintf("%s_%s.wav", playerId, demoID))

        if format == "VOICEDATA_FORMAT_OPUS" {
            err = opusToWav(voiceData, wavFilePath)
            if err != nil {
                log.Printf("Error processing opus data for player %s: %v", playerId, err)
                continue
            }
        } else if format == "VOICEDATA_FORMAT_STEAM" {
            err = convertAudioDataToWavFiles(voiceData, wavFilePath)
            if err != nil {
                log.Printf("Error processing steam format data for player %s: %v", playerId, err)
                continue
            }
        }
    }

    return nil
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

func convertAudioDataToWavFiles(payloads [][]byte, fileName string) error {
    // This sample rate can be set using data from the VoiceData net message.
    // But every demo processed has used 24000 and is single channel.
    voiceDecoder, err := decoder.NewOpusDecoder(24000, 1)
    if err != nil {
        return fmt.Errorf("failed to create decoder: %v", err)
    }

    o := make([]int, 0, 1024)

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

            converted := make([]int, len(pcm))
            for i, v := range pcm {
                // Float32 buffer implementation is wrong in go-audio, so we have to convert to int before encoding
                converted[i] = int(v * 2147483647)
            }

            o = append(o, converted...)
        }
    }

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

func opusToWav(data [][]byte, wavName string) error {
    opusDecoder, err := decoder.NewDecoder(48000, 1)
    if err != nil {
        return fmt.Errorf("failed to create opus decoder: %v", err)
    }

    var pcmBuffer []int

    for _, d := range data {
        pcm, err := decoder.Decode(opusDecoder, d)
        if err != nil {
            log.Printf("Error decoding opus data: %v", err)
            continue
        }

        pp := make([]int, len(pcm))
        for i, p := range pcm {
            pp[i] = int(p * 2147483647)
        }

        pcmBuffer = append(pcmBuffer, pp...)
    }

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
