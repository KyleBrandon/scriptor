package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/sashabaranov/go-openai"
)

type handlerConfig struct {
	store         database.DocumentStore
	s3Client      *s3.Client
	chatgptClient *openai.Client
}

var (
	BucketName string = types.S3_BUCKET_NAME
	initOnce   sync.Once
	cfg        *handlerConfig
)

const (
	SYSTEM_MESSAGE = "You are an AI that processes Markdown text. Your task is to clean up the input by fixing Markdown syntax, correcting spelling and grammar, and ensuring proper formatting. Do NOT include any extra explanations, comments, or surrounding text—only return the valid Markdown output."
	CHAT_PROMPT    = "Here is a Markdown file that was generated via OCR. Fix the Markdown formatting, correct any spelling and grammar errors, and ensure the syntax is valid. Do not add any explanations,comments, and do not surround the document text in a markdown code block. ONLY RETURN THE CLEANED MARKDOWN CONTENT AND NOTHING ELSE:\n\n%s"

	HEADER_TEMPLATE = `---
id: "%s"
aliases: []
tags:
  - daily-notes
---
`
	FOOTER_TEMPLATE = "![[/Users/kyle.brandon/journal/attachments/%s]]"
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

	cfg.chatgptClient, err = util.CreateChatGPTClient(ctx, awsCfg)
	if err != nil {
		slog.Error("Failed to create a client to the ChatGPT API", "error", err)
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

	chatgptStage, err := cfg.store.StartDocumentStage(
		ctx,
		event.DocumentID,
		types.DOCUMENT_STAGE_CHATGPT,
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

	// // Create a prompt for GPT to clean up the Markdown
	prompt := fmt.Sprintf(CHAT_PROMPT, content)

	// // Call the ChatGPT API
	gptResp, err := cfg.chatgptClient.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: openai.GPT4o,
			Messages: []openai.ChatCompletionMessage{
				{Role: "system", Content: SYSTEM_MESSAGE},
				{Role: "user", Content: prompt},
			},
			Temperature: 0.2, // Keep responses precise
		},
	)
	if err != nil {
		slog.Error(
			"ChatGPT API error",
			"docName",
			prevStage.OriginalFileName,
			"error",
			err,
		)
		return ret, err
	}

	// // Get the cleaned-up text
	buffer := gptResp.Choices[0].Message.Content

	// For some reason ChatGPT will occasionally surround the entire processed output with
	// a Markdown code block. Check to see if the document is surrounded in a code block.
	// If so, remove it.
	cleanedMarkdown := strings.TrimPrefix(
		strings.TrimSuffix(string(buffer), "```"),
		"```markdown",
	)

	// TODO: This should be a configuration
	// build the header and footer for the note
	header := fmt.Sprintf(
		HEADER_TEMPLATE,
		util.GetNamePart(prevStage.OriginalFileName),
	)
	footer := fmt.Sprintf(FOOTER_TEMPLATE, prevStage.OriginalFileName)

	// We want to append a link to the original scanned PDF at the end of the note
	output := fmt.Sprintf("%s\n\n%s\n\n%s", header, cleanedMarkdown, footer)

	// get the bytes for the markdown file
	body := []byte(output)

	// Get the original document name w/o extension
	documentName := util.GetNamePart(prevStage.OriginalFileName)

	chatgptStage.StageFileName = fmt.Sprintf(
		"%s-%d.md",
		documentName,
		time.Now().Unix(),
	)
	chatgptStage.S3Key = fmt.Sprintf(
		"%s/%s",
		chatgptStage.Stage,
		chatgptStage.StageFileName,
	)

	//
	_, err = cfg.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(BucketName),
		Key:           aws.String(chatgptStage.S3Key),
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
			chatgptStage.S3Key,
			"error",
			err,
		)
		return ret, err
	}

	// Update the stage to complete
	err = cfg.store.CompleteDocumentStage(ctx, chatgptStage)
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
	ret.Stage = types.DOCUMENT_STAGE_CHATGPT

	return ret, nil
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(process)
}
