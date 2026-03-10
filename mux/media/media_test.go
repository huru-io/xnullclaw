package media

import (
	"testing"
)

func TestParseNoMarkers(t *testing.T) {
	text := "Hello, this is a normal response."
	clean, atts := Parse(text)
	if clean != text {
		t.Fatalf("expected original text, got: %q", clean)
	}
	if len(atts) != 0 {
		t.Fatalf("expected 0 attachments, got %d", len(atts))
	}
}

func TestParseSingleImage(t *testing.T) {
	text := "Here is the image: [IMAGE:/nullclaw-data/output.png] Done."
	clean, atts := Parse(text)
	if clean != "Here is the image:  Done." {
		t.Fatalf("unexpected clean text: %q", clean)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if atts[0].Type != TypeImage {
		t.Fatalf("expected IMAGE, got %s", atts[0].Type)
	}
	if atts[0].Path != "/nullclaw-data/output.png" {
		t.Fatalf("unexpected path: %s", atts[0].Path)
	}
}

func TestParseMultipleMarkers(t *testing.T) {
	text := "Results:\n[IMAGE:/tmp/photo.jpg]\n[FILE:/tmp/report.pdf]\n[VOICE:/tmp/audio.ogg]"
	clean, atts := Parse(text)
	if len(atts) != 3 {
		t.Fatalf("expected 3 attachments, got %d", len(atts))
	}

	expected := []struct {
		typ  MarkerType
		path string
	}{
		{TypeImage, "/tmp/photo.jpg"},
		{TypeFile, "/tmp/report.pdf"},
		{TypeVoice, "/tmp/audio.ogg"},
	}

	for i, e := range expected {
		if atts[i].Type != e.typ {
			t.Errorf("att[%d]: expected type %s, got %s", i, e.typ, atts[i].Type)
		}
		if atts[i].Path != e.path {
			t.Errorf("att[%d]: expected path %s, got %s", i, e.path, atts[i].Path)
		}
	}

	if clean == "" {
		t.Fatal("clean text should not be empty (has 'Results:')")
	}
}

func TestParseAllTypes(t *testing.T) {
	text := "[IMAGE:/a][FILE:/b][DOCUMENT:/c][VIDEO:/d][AUDIO:/e][VOICE:/f]"
	_, atts := Parse(text)
	if len(atts) != 6 {
		t.Fatalf("expected 6 attachments, got %d", len(atts))
	}
	types := []MarkerType{TypeImage, TypeFile, TypeDocument, TypeVideo, TypeAudio, TypeVoice}
	for i, typ := range types {
		if atts[i].Type != typ {
			t.Errorf("att[%d]: expected %s, got %s", i, typ, atts[i].Type)
		}
	}
}

func TestParseEmptyPath(t *testing.T) {
	// Paths must start with / so an empty path won't match.
	text := "[IMAGE:] not a marker"
	_, atts := Parse(text)
	if len(atts) != 0 {
		t.Fatalf("expected 0 attachments for empty path, got %d", len(atts))
	}
}

func TestParsePathWithSpaces(t *testing.T) {
	text := "[FILE:/tmp/my file.pdf]"
	_, atts := Parse(text)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if atts[0].Path != "/tmp/my file.pdf" {
		t.Fatalf("unexpected path: %q", atts[0].Path)
	}
}
