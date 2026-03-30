package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/KyleBrandon/scriptor/pkg/types"
)

func TestBuildEmailSubject(t *testing.T) {
	got := buildEmailSubject("[Scriptor]", "Weekly Notes")

	if got != "[Scriptor] Weekly Notes" {
		t.Fatalf("buildEmailSubject returned %q", got)
	}
}

func TestBuildRawEmailIncludesBodyAndAttachments(t *testing.T) {
	emailConfig := &types.PostUploadEmailConfig{
		Enabled:       true,
		FromAddress:   "scriptor@brander-tech.com",
		ToAddresses:   []string{"recipient@example.com"},
		ReplyTo:       []string{"reply@example.com"},
		SubjectPrefix: "[Scriptor]",
	}

	rawEmail, err := buildRawEmail(
		emailConfig,
		buildEmailSubject(emailConfig.SubjectPrefix, "Weekly Notes"),
		buildEmailBody(
			&types.Document{
				Name:       "Weekly Notes.pdf",
				SourceType: types.DOCUMENT_SOURCE_GOOGLE_DRIVE,
			},
			time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC),
		),
		[]emailAttachment{
			{
				FileName:    "Weekly Notes.pdf",
				ContentType: "application/pdf",
				Content:     []byte("pdf-bytes"),
			},
			{
				FileName:    "Weekly Notes.md",
				ContentType: "text/markdown; charset=utf-8",
				Content:     []byte("# heading"),
			},
		},
	)
	if err != nil {
		t.Fatalf("buildRawEmail returned error: %v", err)
	}

	message, err := mail.ReadMessage(bytes.NewReader(rawEmail))
	if err != nil {
		t.Fatalf("ReadMessage returned error: %v", err)
	}

	if got := message.Header.Get("From"); got != "scriptor@brander-tech.com" {
		t.Fatalf("unexpected From header: %q", got)
	}

	if got := message.Header.Get("To"); got != "recipient@example.com" {
		t.Fatalf("unexpected To header: %q", got)
	}

	mediaType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("ParseMediaType returned error: %v", err)
	}

	if mediaType != "multipart/mixed" {
		t.Fatalf("unexpected media type: %q", mediaType)
	}

	reader := multipart.NewReader(message.Body, params["boundary"])

	part, err := reader.NextPart()
	if err != nil {
		t.Fatalf("NextPart returned error for body part: %v", err)
	}

	bodyBytes, err := io.ReadAll(part)
	if err != nil {
		t.Fatalf("ReadAll returned error for body part: %v", err)
	}

	body := string(bodyBytes)
	if want := "Scriptor finished processing a document."; !bytes.Contains(bodyBytes, []byte(want)) {
		t.Fatalf("body did not contain %q: %q", want, body)
	}

	attachments := map[string]string{}

	for {
		part, err = reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart returned error: %v", err)
		}

		_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil {
			t.Fatalf("ParseMediaType returned error for part: %v", err)
		}

		encoded, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("ReadAll returned error for attachment part: %v", err)
		}

		decoded, err := base64.StdEncoding.DecodeString(
			strings.ReplaceAll(string(encoded), "\r\n", ""),
		)
		if err != nil {
			t.Fatalf("DecodeString returned error: %v", err)
		}

		attachments[params["filename"]] = string(decoded)
	}

	if got := attachments["Weekly Notes.pdf"]; got != "pdf-bytes" {
		t.Fatalf("unexpected PDF attachment contents: %q", got)
	}

	if got := attachments["Weekly Notes.md"]; got != "# heading" {
		t.Fatalf("unexpected Markdown attachment contents: %q", got)
	}
}

func TestBuildRawEmailRejectsOversizedMessages(t *testing.T) {
	emailConfig := &types.PostUploadEmailConfig{
		Enabled:     true,
		FromAddress: "scriptor@brander-tech.com",
		ToAddresses: []string{"recipient@example.com"},
	}

	largeAttachment := bytes.Repeat([]byte("a"), 31*1024*1024)

	_, err := buildRawEmail(
		emailConfig,
		"Large Document",
		"hello",
		[]emailAttachment{
			{
				FileName:    "large.pdf",
				ContentType: "application/pdf",
				Content:     largeAttachment,
			},
		},
	)
	if err == nil {
		t.Fatal("expected oversized email to return an error")
	}
}
