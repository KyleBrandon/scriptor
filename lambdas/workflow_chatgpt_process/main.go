package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/sashabaranov/go-openai"
)

type chatgptConfig struct {
	store    database.DocumentStore
	s3Client *s3.Client
	apiKey   string
}

var (
	BucketName string = types.S3_BUCKET_NAME
	cfg        *chatgptConfig
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

func (cfg *chatgptConfig) process(ctx context.Context, event types.DocumentStep) (types.DocumentStep, error) {
	slog.Debug(">>process")
	defer slog.Debug("<<process")

	ret := types.DocumentStep{}

	var err error
	cfg.store, err = util.VerifyDocumentStore(cfg.store)
	if err != nil {
		slog.Error("Failed to verify the DynamoDB client", "error", err)
		return ret, err
	}

	// query the previous stage information
	prevStage, err := cfg.store.GetDocumentStage(event.DocumentID, event.Stage)
	if err != nil {
		slog.Error("Failed to get the previous stage information", "id", event.DocumentID, "stage", event.Stage, "error", err)
		return ret, err
	}

	chatgptStage, err := cfg.store.StartDocumentStage(event.DocumentID, types.DOCUMENT_STAGE_CHATGPT, prevStage.OriginalFileName)
	if err != nil {
		slog.Error("Failed to save the document processing stage", "docName", prevStage.OriginalFileName, "error", err)
		return ret, err
	}

	resp, err := cfg.s3Client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(types.S3_BUCKET_NAME),
		Key:    aws.String(prevStage.S3Key),
	})
	if err != nil {
		slog.Error("Failed to get the document from S3", "docName", prevStage.OriginalFileName, "error", err)
		return ret, err
	}

	defer resp.Body.Close()

	// Initialize OpenAI client
	client := openai.NewClient(cfg.apiKey)

	content, err := io.ReadAll(resp.Body)
	if err != err {
		slog.Error("Failed to read the input document to clean up", "docName", prevStage.OriginalFileName, "error", err)
		return ret, err
	}

	// // Create a prompt for GPT to clean up the Markdown
	prompt := fmt.Sprintf(CHAT_PROMPT, content)

	// // Call the ChatGPT API
	gptResp, err := client.CreateChatCompletion(
		context.TODO(),
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
		slog.Error("ChatGPT API error", "docName", prevStage.OriginalFileName, "error", err)
		return ret, err
	}

	// // Get the cleaned-up text
	buffer := gptResp.Choices[0].Message.Content

	// For some reason ChatGPT will occasionally surround the entire processed output with
	// a Markdown code block. Check to see if the document is surrounded in a code block.
	// If so, remove it.
	cleanedMarkdown := strings.TrimPrefix(strings.TrimSuffix(string(buffer), "```"), "```markdown")

	// TODO: This should be a configuration
	// build the header and footer for the note
	header := fmt.Sprintf(HEADER_TEMPLATE, util.GetDocumentName(prevStage.OriginalFileName))
	footer := fmt.Sprintf(FOOTER_TEMPLATE, prevStage.OriginalFileName)

	// We want to append a link to the original scanned PDF at the end of the note
	output := fmt.Sprintf("%s\n\n%s\n\n%s", header, cleanedMarkdown, footer)

	// get the bytes for the markdown file
	body := []byte(output)

	// Get the original document name w/o extension
	documentName := util.GetDocumentName(prevStage.OriginalFileName)

	chatgptStage.StageFileName = fmt.Sprintf("%s-%d.md", documentName, time.Now().Unix())
	chatgptStage.S3Key = fmt.Sprintf("%s/%s", chatgptStage.Stage, chatgptStage.StageFileName)

	//
	_, err = cfg.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(BucketName),
		Key:           aws.String(chatgptStage.S3Key),
		Body:          bytes.NewReader(body),
		ContentType:   aws.String("text/markdown"),
		ContentLength: aws.Int64(int64(len(body))),
	})
	if err != nil {
		slog.Error("Failed to save the document in the S3 bucket", "docName", prevStage.OriginalFileName, "key", chatgptStage.S3Key, "error", err)
		return ret, err
	}

	// Update the stage to complete
	err = cfg.store.CompleteDocumentStage(chatgptStage)
	if err != nil {
		slog.Error("Failed to update the processing stage as complete", "docName", prevStage.OriginalFileName, "error", err)
		return ret, err
	}

	// read doc from bucket
	ret.DocumentID = event.DocumentID
	ret.Stage = types.DOCUMENT_STAGE_CHATGPT

	return ret, nil
}

func getChatgptKeys() (string, error) {
	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		os.Exit(1)
	}

	svc := secretsmanager.NewFromConfig(awsCfg)

	secretName := types.CHATGPT_SECRETS
	input := &secretsmanager.GetSecretValueInput{SecretId: &secretName}

	result, err := svc.GetSecretValue(context.TODO(), input)
	if err != nil {
		return "", err
	}

	var chatgptSecrets types.ChatGptSecrets

	err = json.Unmarshal([]byte(*result.SecretString), &chatgptSecrets)
	if err != nil {
		return "", err
	}

	return chatgptSecrets.ApiKey, nil
}

func init() {
	slog.Debug(">>chatgptLambda.init")
	defer slog.Debug("<<chatgptLambda.init")

	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Failed to load the AWS config", "error", err)
		os.Exit(1)
	}

	apiKey, err := getChatgptKeys()
	if err != nil {
		slog.Error("Failed to get the ChatGPT keys", "error", err)
		os.Exit(1)
	}

	s3Client := s3.NewFromConfig(awsCfg)

	cfg = &chatgptConfig{
		s3Client: s3Client,
		apiKey:   apiKey,
	}
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(cfg.process)
}
