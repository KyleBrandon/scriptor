package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
)

type handlerConfig struct {
	store        database.DocumentStore
	s3Client     *s3.Client
	claudeClient *anthropic.Client
}

var (
	BucketName string = types.S3_BUCKET_NAME
	initOnce   sync.Once
	cfg        *handlerConfig
)

const (
	SYSTEM_MESSAGE = "You are a document restoration specialist. You receive an original PDF and a Markdown transcription produced by OCR. Your job is to produce a corrected Markdown version that faithfully represents the original document. Always prefer what the PDF shows over what the OCR produced. Return only valid Markdown with no commentary."
	CHAT_PROMPT    = `Below is a Markdown file generated from the attached PDF via OCR (Mathpix). Compare it against the original PDF and correct the Markdown so it faithfully represents the source document.

Priority order:
1. **Content accuracy** — Fix misread words, numbers, and characters (e.g. "rn" → "m", "l" → "1", "O" → "0"). Use the PDF as the source of truth.
2. **Structure** — Ensure headings, lists, tables, and block quotes match the PDF layout. Fix broken table alignment, merged or split rows, and incorrect nesting.
3. **Math and symbols** — Verify LaTeX expressions, currency symbols, units, and special characters against the PDF.
4. **Formatting** — Fix Markdown syntax errors, stray artifacts (e.g. random backslashes, repeated characters), and unnecessary line breaks.
5. **Spelling and grammar** — Correct any remaining errors, but do not rephrase or rewrite the author's original text.

Do not add explanations, comments, or wrap the output in a code block. Return ONLY the corrected Markdown.

%s`

	HEADER_TEMPLATE = `---
id: "%s"
aliases: []
tags:
  - reMarkable
---

People:
Projects:
Zettel:

`

	FOOTER_TEMPLATE = "![[attachments/%s]]"
)

// Load all the inital configuration settings for the lambda
func loadConfiguration(ctx context.Context) (*handlerConfig, error) {
	cfg = &handlerConfig{}

	var err error

	cfg.store, err = database.NewDocumentStore(ctx)
	if err != nil {
		slog.Error("Failed to configure the DynamoDB client", "error", err)
		return nil, err
	}

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("Failed to load the AWS config", "error", err)
		return nil, err
	}

	cfg.s3Client = s3.NewFromConfig(awsCfg)

	cfg.claudeClient, err = util.CreateClaudeClient(ctx, awsCfg)
	if err != nil {
		slog.Error("Failed to create a client to the Claude API", "error", err)
		return nil, err
	}

	return cfg, nil
}

// Ensure that the configuration settings are only loaded once
func initLambda(ctx context.Context) error {
	var err error
	initOnce.Do(func() {
		slog.Debug(">>initLambda")
		defer slog.Debug("<<initLambda")

		cfg, err = loadConfiguration(ctx)
	})

	return err
}

func process(
	ctx context.Context,
	event types.DocumentStep,
) (types.DocumentStep, error) {
	slog.Debug(">>process")
	defer slog.Debug("<<process")

	ret := types.DocumentStep{}

	if err := initLambda(ctx); err != nil {
		slog.Error("Failed to initialize the lambda", "error", err)
		return ret, err
	}

	// query the previous stage information
	prevStage, err := cfg.store.GetDocumentStage(
		ctx,
		event.DocumentID,
		event.Stage,
	)
	if err != nil {
		slog.Error(
			"Failed to get the previous stage information",
			"id",
			event.DocumentID,
			"stage",
			event.Stage,
			"error",
			err,
		)
		return ret, err
	}

	// Get the downloaded stage to retrieve the original PDF
	downloadedStage, err := cfg.store.GetDocumentStage(
		ctx,
		event.DocumentID,
		types.DOCUMENT_STAGE_DOWNLOAD,
	)
	if err != nil {
		slog.Error(
			"Failed to get the downloaded stage information",
			"id",
			event.DocumentID,
			"error",
			err,
		)
		return ret, err
	}

	// Download the original PDF from S3
	pdfResp, err := cfg.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(types.S3_BUCKET_NAME),
		Key:    aws.String(downloadedStage.S3Key),
	})
	if err != nil {
		slog.Error(
			"Failed to get the PDF from S3",
			"docName",
			prevStage.OriginalFileName,
			"key",
			downloadedStage.S3Key,
			"error",
			err,
		)
		return ret, err
	}
	defer pdfResp.Body.Close()

	pdfBytes, err := io.ReadAll(pdfResp.Body)
	if err != nil {
		slog.Error(
			"Failed to read the PDF content",
			"docName",
			prevStage.OriginalFileName,
			"error",
			err,
		)
		return ret, err
	}

	pdfBase64 := base64.StdEncoding.EncodeToString(pdfBytes)

	claudeStage, err := cfg.store.StartDocumentStage(
		ctx,
		event.DocumentID,
		types.DOCUMENT_STAGE_CLAUDE,
		prevStage.OriginalFileName,
	)
	if err != nil {
		slog.Error(
			"Failed to save the document processing stage",
			"docName",
			prevStage.OriginalFileName,
			"error",
			err,
		)
		return ret, err
	}

	resp, err := cfg.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(types.S3_BUCKET_NAME),
		Key:    aws.String(prevStage.S3Key),
	})
	if err != nil {
		slog.Error(
			"Failed to get the document from S3",
			"docName",
			prevStage.OriginalFileName,
			"error",
			err,
		)
		return ret, err
	}

	defer resp.Body.Close()

	content, err := io.ReadAll(resp.Body)
	if err != err {
		slog.Error(
			"Failed to read the input document to clean up",
			"docName",
			prevStage.OriginalFileName,
			"error",
			err,
		)
		return ret, err
	}

	// Create a prompt for Claude to clean up the Markdown
	prompt := fmt.Sprintf(CHAT_PROMPT, content)

	// Call the Claude API with the original PDF and Markdown prompt
	claudeResp, err := cfg.claudeClient.Messages.New(
		ctx,
		anthropic.MessageNewParams{
			Model:     anthropic.ModelClaudeSonnet4_5_20250929,
			MaxTokens: 8192,
			System: []anthropic.TextBlockParam{
				{Text: SYSTEM_MESSAGE},
			},
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(
					anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{
						Data: pdfBase64,
					}),
					anthropic.NewTextBlock(prompt),
				),
			},
		},
	)
	if err != nil {
		slog.Error(
			"Claude API error",
			"docName",
			prevStage.OriginalFileName,
			"error",
			err,
		)
		return ret, err
	}

	// Get the cleaned-up text
	buffer := claudeResp.Content[0].Text

	// Safety check: remove markdown code block wrapping if present
	cleanedMarkdown := strings.TrimPrefix(
		strings.TrimSuffix(string(buffer), "```"),
		"```markdown",
	)

	// TODO: This should be a configuration
	// build the header and footer for the note
	name := util.GetNamePart(prevStage.OriginalFileName)
	header := fmt.Sprintf(
		HEADER_TEMPLATE,
		name,
	)
	footer := fmt.Sprintf(FOOTER_TEMPLATE, prevStage.OriginalFileName)

	// We want to append a link to the original scanned PDF at the end of the note
	output := fmt.Sprintf("%s\n\n%s\n\n%s", header, cleanedMarkdown, footer)

	// get the bytes for the markdown file
	body := []byte(output)

	// Get the original document name w/o extension
	documentName := util.GetNamePart(prevStage.OriginalFileName)

	claudeStage.StageFileName = fmt.Sprintf(
		"%s-%d.md",
		documentName,
		time.Now().Unix(),
	)
	claudeStage.S3Key = fmt.Sprintf(
		"%s/%s",
		claudeStage.Stage,
		claudeStage.StageFileName,
	)

	//
	_, err = cfg.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(BucketName),
		Key:           aws.String(claudeStage.S3Key),
		Body:          bytes.NewReader(body),
		ContentType:   aws.String("text/markdown"),
		ContentLength: aws.Int64(int64(len(body))),
	})
	if err != nil {
		slog.Error(
			"Failed to save the document in the S3 bucket",
			"docName",
			prevStage.OriginalFileName,
			"key",
			claudeStage.S3Key,
			"error",
			err,
		)
		return ret, err
	}

	// Update the stage to complete
	err = cfg.store.CompleteDocumentStage(ctx, claudeStage)
	if err != nil {
		slog.Error(
			"Failed to update the processing stage as complete",
			"docName",
			prevStage.OriginalFileName,
			"error",
			err,
		)
		return ret, err
	}

	// read doc from bucket
	ret.NotificationID = event.NotificationID
	ret.DocumentID = event.DocumentID
	ret.Stage = types.DOCUMENT_STAGE_CLAUDE

	return ret, nil
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(process)
}
