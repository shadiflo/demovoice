# CS DEMO Voice Extractor

Example code for exporting players voices from CS2 demos into WAV files.

**Valve Matchmaking demos do not contain voice audio data, as such there is nothing to extract from MM demo files.**

## Purpose and goals
The intention of this project is not to be an end user tool for bulk or batch processing demos and extracting voice data.

However, this should serve as a guideline for how to process the audio data as pulled by [demoinfocs-golang](https://github.com/markus-wa/demoinfocs-golang). People using that tool to process their demos who wish to also pull voice data can leverage this sample to build that audio processing into their demo processing tools.

## Running locally
1. Install Go 1.26.2 or newer.
2. Install the native Opus dependencies for your OS.
3. Copy `.env` with your Faceit keys if you want Faceit lookup/download support:
   ```sh
   FACEIT_API_KEY=...
   FACEIT_DOWNLOAD_API_KEY=...
   API_KEY=...
   ```
4. Start the web app:
   ```sh
   go run .
   ```
5. Open `http://localhost:9000`.

The app writes uploaded demos to `upload/` and extracted audio/metadata to `output/`.

## Dependencies
This project uses cgo through `gopkg.in/hraban/opus.v2`, so Go alone is not enough for audio extraction. You also need a C compiler, `pkg-config`, and the Opus development libraries.

Linux:
```sh
sudo apt-get install build-essential pkg-config libopus-dev libopusfile-dev
```

Mac:
```sh
brew install pkg-config opus opusfile
```

Windows:
- Go can run the server, but audio extraction needs a Windows cgo toolchain plus Opus headers/libs.
- The easiest production path is still building/running on Linux, or testing the full audio path on the VPS.

## Building for a VPS
```sh
make build-vps
```

On the VPS, install the Linux dependencies above before running the binary.

# Acknowledgements

Thanks to [@rumblefrog](https://github.com/rumblefrog) for all their help in getting this working. Check out this excellent blog post about [Reversing Steam Voice Codec](https://zhenyangli.me/posts/reversing-steam-voice-codec/) and their work on [Source Chat Relay](https://github.com/rumblefrog/source-chat-relay)

This sample relies on [demoinfocs-golang](https://github.com/markus-wa/demoinfocs-golang). Thank you to [@markus-wa](https://github.com/markus-wa), [@akiver](https://github.com/akiver) and all the contributors there.

Special thanks to [@DandrewsDev](https://github.com/DandrewsDev/CS2VoiceData) for providing the CS2VoiceData code that made this project possible.
