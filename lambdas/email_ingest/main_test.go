package main

import (
	"strings"
	"testing"
)

func TestReadMessageBodyMultipartAlternative(t *testing.T) {
	header := map[string][]string{
		"Content-Type": {"multipart/alternative; boundary=test-boundary"},
	}

	body := strings.Join([]string{
		"--test-boundary",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		"Plain text body",
		"--test-boundary",
		"Content-Type: text/html; charset=UTF-8",
		"",
		`<html><body><a href="https://www.amazon.com/gp/f.html?U=https%3A%2F%2Fkindle-content-requests-prod.s3.amazonaws.com%2Fabc%2Fjournal.pdf%3FX-Amz-Algorithm%3DAWS4-HMAC-SHA256">Download PDF</a></body></html>`,
		"--test-boundary--",
		"",
	}, "\r\n")

	htmlBody, textBody, err := readMessageBody(header, strings.NewReader(body))
	if err != nil {
		t.Fatalf("readMessageBody returned an error: %v", err)
	}

	if htmlBody == "" {
		t.Fatalf("expected html body to be populated")
	}

	if textBody == "" {
		t.Fatalf("expected text body to be populated")
	}
}

func TestExtractKindleDownloadURLFromHTML(t *testing.T) {
	email := &parsedEmail{
		HTMLBody: `<html><body><a href="https://www.amazon.com/gp/f.html?C=abc&U=https%3A%2F%2Fkindle-content-requests-prod.s3.amazonaws.com%2F9b358f02-b946-47f5-82c5-b9e2e43ce5f3%2Fjournal-2026-03-11-17-57.pdf%3FX-Amz-Date%3D20260311T225758Z%26X-Amz-Expires%3D604800">Download PDF</a></body></html>`,
	}

	downloadURL, err := extractKindleDownloadURL(email)
	if err != nil {
		t.Fatalf("extractKindleDownloadURL returned an error: %v", err)
	}

	if got, want := downloadURL.Host, "kindle-content-requests-prod.s3.amazonaws.com"; got != want {
		t.Fatalf("unexpected host: got %q want %q", got, want)
	}

	if got, want := downloadURL.Path, "/9b358f02-b946-47f5-82c5-b9e2e43ce5f3/journal-2026-03-11-17-57.pdf"; got != want {
		t.Fatalf("unexpected path: got %q want %q", got, want)
	}
}

func TestBuildKindleSourceKeyStripsSignature(t *testing.T) {
	downloadURL, err := resolveDownloadURL(
		"https://www.amazon.com/gp/f.html?U=https%3A%2F%2Fkindle-content-requests-prod.s3.amazonaws.com%2Ffolder%2Fjournal.pdf%3FX-Amz-Date%3D20260311T225758Z%26X-Amz-Expires%3D604800%26X-Amz-Signature%3Dabc",
	)
	if err != nil {
		t.Fatalf("resolveDownloadURL returned an error: %v", err)
	}

	if got, want := buildKindleSourceKey(downloadURL), "kindle_email:kindle-content-requests-prod.s3.amazonaws.com/folder/journal.pdf"; got != want {
		t.Fatalf("unexpected source key: got %q want %q", got, want)
	}
}
