package types

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
)

func TestDocumentMarshalOmitsEmptyGoogleID(t *testing.T) {
	document := Document{
		ID:           "doc-1",
		SourceType:   DOCUMENT_SOURCE_KINDLE_EMAIL,
		SourceKey:    "kindle_email:example/file.pdf",
		Name:         "file.pdf",
		Size:         123,
		CreatedTime:  time.Now().UTC(),
		ModifiedTime: time.Now().UTC(),
	}

	item, err := attributevalue.MarshalMap(document)
	if err != nil {
		t.Fatalf("MarshalMap returned error: %v", err)
	}

	if _, ok := item["google_id"]; ok {
		t.Fatalf("expected google_id to be omitted for non-Google documents")
	}
}

func TestDocumentMarshalIncludesGoogleIDWhenPresent(t *testing.T) {
	document := Document{
		ID:           "doc-2",
		SourceType:   DOCUMENT_SOURCE_GOOGLE_DRIVE,
		SourceKey:    "google_drive:file-123",
		GoogleID:     "file-123",
		Name:         "file.pdf",
		Size:         123,
		CreatedTime:  time.Now().UTC(),
		ModifiedTime: time.Now().UTC(),
	}

	item, err := attributevalue.MarshalMap(document)
	if err != nil {
		t.Fatalf("MarshalMap returned error: %v", err)
	}

	if _, ok := item["google_id"]; !ok {
		t.Fatalf("expected google_id to be present for Google Drive documents")
	}
}
