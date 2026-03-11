// Package media handles parsing and processing of media markers in agent responses.
//
// Agents may embed markers like [IMAGE:/path/to/file] or [FILE:/path/to/file] in their
// text responses. This package extracts those markers so the mux can send the referenced
// files via Telegram alongside the text.
package media

import (
	"regexp"
	"strings"
)

// MarkerType identifies the kind of media attachment.
type MarkerType string

const (
	TypeImage    MarkerType = "IMAGE"
	TypeFile     MarkerType = "FILE"
	TypeDocument MarkerType = "DOCUMENT"
	TypeVideo    MarkerType = "VIDEO"
	TypeAudio    MarkerType = "AUDIO"
	TypeVoice    MarkerType = "VOICE"
)

// Attachment represents a media attachment extracted from agent response text.
type Attachment struct {
	Type MarkerType
	Path string // path inside the agent container
}

// markerRe matches [TYPE:/path] markers in text.
var markerRe = regexp.MustCompile(`\[(IMAGE|FILE|DOCUMENT|VIDEO|AUDIO|VOICE):(/[^\]]+)\]`)

// Parse extracts all media markers from text and returns the cleaned text
// (with markers removed) and the list of attachments found.
func Parse(text string) (cleanText string, attachments []Attachment) {
	matches := markerRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	var result strings.Builder
	last := 0

	for _, m := range matches {
		// m[0]:m[1] = full match, m[2]:m[3] = type, m[4]:m[5] = path
		result.WriteString(text[last:m[0]])

		markerType := MarkerType(text[m[2]:m[3]])
		path := text[m[4]:m[5]]

		attachments = append(attachments, Attachment{
			Type: markerType,
			Path: path,
		})

		last = m[1]
	}
	result.WriteString(text[last:])

	cleanText = strings.TrimSpace(result.String())
	return cleanText, attachments
}
