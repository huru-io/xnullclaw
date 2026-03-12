// Package voice handles speech-to-text and text-to-speech via the OpenAI API.
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const baseURL = "https://api.openai.com/v1"

// voiceClient is used for all voice API requests. The timeout prevents
// hanging indefinitely if the OpenAI API is slow (voice files can be large).
var voiceClient = &http.Client{Timeout: 120 * time.Second}

// Transcribe sends an audio file to the OpenAI Whisper API and returns the transcribed text.
func Transcribe(ctx context.Context, audioPath, apiKey, model string) (string, error) {
	if model == "" {
		model = "whisper-1"
	}

	f, err := os.Open(audioPath)
	if err != nil {
		return "", fmt.Errorf("voice: open audio %s: %w", audioPath, err)
	}
	defer f.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	// Model field.
	if err := w.WriteField("model", model); err != nil {
		return "", fmt.Errorf("voice: write model field: %w", err)
	}

	// File field.
	part, err := w.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", fmt.Errorf("voice: create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", fmt.Errorf("voice: copy audio data: %w", err)
	}

	if err := w.Close(); err != nil {
		return "", fmt.Errorf("voice: close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", fmt.Errorf("voice: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := voiceClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("voice: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("voice: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("voice: API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("voice: parse response: %w", err)
	}

	return result.Text, nil
}

// Synthesize converts text to speech using the OpenAI TTS API, writing the result to destPath.
// The model parameter defaults to "tts-1" when empty.
func Synthesize(ctx context.Context, text, apiKey, model, voice, destPath string) error {
	if voice == "" {
		voice = "nova"
	}
	if model == "" {
		model = "tts-1"
	}

	payload := map[string]any{
		"model": model,
		"input": text,
		"voice": voice,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("voice: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/audio/speech", bytes.NewReader(payloadJSON))
	if err != nil {
		return fmt.Errorf("voice: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := voiceClient.Do(req)
	if err != nil {
		return fmt.Errorf("voice: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("voice: API error %d: %s", resp.StatusCode, string(body))
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("voice: create output file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("voice: write audio: %w", err)
	}

	return nil
}
