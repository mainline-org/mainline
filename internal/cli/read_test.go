package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestContextRetrievalRequestAddsTrailingFileArgs(t *testing.T) {
	req, err := contextRetrievalRequestFromFlags(false, []string{"src/alpha.go"}, "", 0, []string{"src/beta.go"})
	if err != nil {
		t.Fatalf("context request: %v", err)
	}
	if req == nil {
		t.Fatal("expected retrieval request")
	}
	if req.Mode != "files" {
		t.Fatalf("expected files mode, got %q", req.Mode)
	}
	want := []string{"src/alpha.go", "src/beta.go"}
	if !reflect.DeepEqual(req.Files, want) {
		t.Fatalf("files mode should include trailing path args: got %#v want %#v", req.Files, want)
	}
}

func TestContextRetrievalRequestPreservesRepeatedAndCommaFiles(t *testing.T) {
	req, err := contextRetrievalRequestFromFlags(false, []string{"src/alpha.go", "src/beta.go"}, "", 7, []string{"src/gamma.go"})
	if err != nil {
		t.Fatalf("context request: %v", err)
	}
	want := []string{"src/alpha.go", "src/beta.go", "src/gamma.go"}
	if !reflect.DeepEqual(req.Files, want) {
		t.Fatalf("files mode should preserve flag-parsed files and append trailing args: got %#v want %#v", req.Files, want)
	}
	if req.Limit != 7 {
		t.Fatalf("expected limit 7, got %d", req.Limit)
	}
}

func TestContextRetrievalRequestRejectsTrailingArgsOutsideFilesMode(t *testing.T) {
	_, err := contextRetrievalRequestFromFlags(false, nil, "auth", 0, []string{"src/auth.go"})
	if err == nil {
		t.Fatal("expected trailing arg rejection for query mode")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("expected unexpected argument error, got %v", err)
	}
}

func TestContextRetrievalRequestRejectsMultipleModes(t *testing.T) {
	_, err := contextRetrievalRequestFromFlags(true, nil, "auth", 0, nil)
	if err == nil {
		t.Fatal("expected multiple mode rejection")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected exactly-one-mode error, got %v", err)
	}
}

func TestContextRetrievalRequestNoModeKeepsLegacyContext(t *testing.T) {
	req, err := contextRetrievalRequestFromFlags(false, nil, "", 0, nil)
	if err != nil {
		t.Fatalf("context request: %v", err)
	}
	if req != nil {
		t.Fatalf("expected nil request for legacy context mode, got %+v", req)
	}
}
