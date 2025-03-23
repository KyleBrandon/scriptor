package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// Mathpix API endpoint
const (
	MathpixPdfApiURL = "https://api.mathpix.com/v3/pdf"

	// Polling interval (seconds)
	MathpixPollInterval = 5
)

type (
	MathpixErrorInfo struct {
		ID      string `json:"id,omitempty"`
		Message string `json:"message,omitempty"`
	}

	// UploadResponse represents the initial response from Mathpix after uploading a PDF
	UploadResponse struct {
		PdfID     string           `json:"pdf_id"`
		Error     string           `json:"error,omitempty"`
		ErrorInfo MathpixErrorInfo `json:"error_info,omitempty"`
	}

	// PollResponse represents the response when polling for PDF processing results
	PollResponse struct {
		Status      string `json:"status"`
		PdfMarkdown string `json:"pdf_md,omitempty"`
	}

	mathpixConfig struct {
		store         database.ScriptorStore
		s3Client      *s3.Client
		mathpixAppID  string
		mathpixAppKey string
	}
)

var (
	BucketName string = types.S3_BUCKET_NAME
	cfg        *mathpixConfig
)

func (cfg *mathpixConfig) doRequestAndReadAll(req *http.Request) ([]byte, error) {

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode > 299 {
		return nil, fmt.Errorf("request failed with status_code=%d and status=%s", resp.StatusCode, resp.Status)
	}

	// Parse response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return respBody, nil
}

func (cfg *mathpixConfig) newRequest(method string, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("app_id", cfg.mathpixAppID)
	req.Header.Set("app_key", cfg.mathpixAppKey)

	return req, nil
}

// PollForResults polls Mathpix API for PDF processing status
func (cfg *mathpixConfig) pollForResults(pdfID string) error {
	pollURL := fmt.Sprintf("%s/%s", MathpixPdfApiURL, pdfID)

	// TODO: This would run forever
	for {
		req, err := cfg.newRequest("GET", pollURL, nil)
		if err != nil {
			slog.Error("Failed to create GET request for mathpix document status", "error", err)
			return err
		}

		bodyContents, err := cfg.doRequestAndReadAll(req)
		if err != nil {
			slog.Error("Failed to send GET request for mathpix documetn status", "error", err)
			return err
		}

		// Parse JSON
		var pollResp PollResponse
		err = json.Unmarshal(bodyContents, &pollResp)
		if err != nil {
			slog.Error("Failed to unmarshal mathpix document status", "body", string(bodyContents), "error", err)
			return err
		}

		slog.Debug("Mathpix", "pollStatus", pollResp.Status)

		// If processing is done, return the markdown text
		switch pollResp.Status {
		case "completed":
			return nil
		case "error":
			return fmt.Errorf("mathpix PDF processing failed")
		}

		// Wait before polling again
		time.Sleep(MathpixPollInterval * time.Second)
	}
}

func (cfg *mathpixConfig) queryConversionResults(pdfID string) ([]byte, error) {
	resultsURL := fmt.Sprintf("%s/%s.md", MathpixPdfApiURL, pdfID)

	req, err := cfg.newRequest("GET", resultsURL, nil)
	if err != nil {
		slog.Error("Failed to crate GET request for mathpix document status", "error", err)
		return nil, err
	}

	body, err := cfg.doRequestAndReadAll(req)
	if err != nil {
		slog.Error("Failed to send GET request for mathpix documetn status", "error", err)
		return nil, err
	}

	return body, nil
}

func (cfg *mathpixConfig) sendDocumentToMathpix(prevStage types.DocumentProcessingStage) (string, error) {
	// get the input file form S3
	resp, err := cfg.s3Client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(types.S3_BUCKET_NAME),
		Key:    aws.String(prevStage.S3Key),
	})
	if err != nil {
		slog.Error("Failed to get the document from S3", "error", err)
		return "", err
	}

	defer resp.Body.Close()

	// Create multipart form data
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", prevStage.StageFileName)
	if err != nil {
		slog.Error("Failed to create form file", "error", err)
		return "", err
	}

	// copy the document input to the request body
	_, err = io.Copy(part, resp.Body)
	if err != nil {
		slog.Error("Failed to copy file to form part", "error", err)
		return "", err
	}
	writer.Close()

	// Create HTTP request
	req, err := cfg.newRequest("POST", MathpixPdfApiURL, body)
	if err != nil {
		slog.Error("Failed to create POST request for mathpix API", "error", err)
		return "", err
	}

	// Set additional headers
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// send the request
	respBody, err := cfg.doRequestAndReadAll(req)
	if err != nil {
		slog.Error("Failed to send mathpix request", "error", err)
		return "", err
	}

	// Process the response for the PDF id
	var uploadResp UploadResponse
	err = json.Unmarshal(respBody, &uploadResp)
	if err != nil {
		slog.Error("Failed to unmarshal mathpix response", "error", err)
		return "", err
	}

	if len(uploadResp.Error) != 0 {
		return "", fmt.Errorf("mathpix error: %s, ErrorInfo.ID=%s, ErrorInfo.Message=%s", uploadResp.Error, uploadResp.ErrorInfo.ID, uploadResp.ErrorInfo.Message)
	}

	return uploadResp.PdfID, nil
}

func (cfg *mathpixConfig) process(ctx context.Context, event types.DocumentStep) (types.DocumentStep, error) {
	slog.Debug(">>process")
	defer slog.Debug("<<process")

	slog.Info("mathpixLambda stage input", "event", event)

	ret := types.DocumentStep{}

	var err error
	cfg.store, err = util.VerifyStoreConnection(cfg.store)
	if err != nil {
		slog.Error("Failed to verify the DynamoDB client", "error", err)
		return ret, err
	}

	// query the previous stage information
	prevStage, err := cfg.store.GetDocumentStage(event.ID, event.Stage)
	if err != nil {
		slog.Error("Failed to get the previous stage information", "id", event.ID, "stage", event.Stage, "error", err)
		return ret, err
	}

	slog.Info("process document", "docName", prevStage.OriginalFileName)

	// create the mathpix stage entry
	mathpixStage, err := cfg.store.StartDocumentStage(event.ID, types.DOCUMENT_STAGE_MATHPIX, prevStage.OriginalFileName)
	if err != nil {
		slog.Error("Failed to start the Mathpix document processing stage", "docName", prevStage.OriginalFileName, "error", err)
		return ret, err
	}

	// Upload PDF to Mathpix
	pdfID, err := cfg.sendDocumentToMathpix(prevStage)
	if err != nil {
		slog.Error("Error uploading PDF", "docName", prevStage.OriginalFileName, "error", err)
		return ret, err
	}

	// Poll for results
	err = cfg.pollForResults(pdfID)
	if err != nil {
		slog.Error("Error getting results", "docName", prevStage.OriginalFileName, "error", err)
		return ret, err
	}

	body, err := cfg.queryConversionResults(pdfID)
	if err != nil {
		slog.Error("Failed to query conversion results", "docName", prevStage.OriginalFileName, "error", err)
		return ret, err

	}

	// Get the original document name w/o extension
	documentName := util.GetDocumentName(prevStage.OriginalFileName)

	mathpixStage.StageFileName = fmt.Sprintf("%s-%d.md", documentName, time.Now().Unix())
	mathpixStage.S3Key = fmt.Sprintf("%s/%s", mathpixStage.Stage, mathpixStage.StageFileName)
	_, err = cfg.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(BucketName),
		Key:           aws.String(mathpixStage.S3Key),
		Body:          bytes.NewReader(body),
		ContentType:   aws.String("text/markdown"),
		ContentLength: aws.Int64(int64(len(body))),
	})
	if err != nil {
		slog.Error("Failed to save the document in the S3 bucket", "docName", prevStage.OriginalFileName, "key", mathpixStage.S3Key, "error", err)
		return ret, err
	}

	// Update the stage to complete

	err = cfg.store.CompleteDocumentStage(mathpixStage)
	if err != nil {
		slog.Error("Failed to update the processing stage as complete", "docName", prevStage.OriginalFileName, "error", err)
		return ret, err
	}

	// pass the step info to the next stage
	ret.ID = event.ID
	ret.Stage = types.DOCUMENT_STAGE_MATHPIX

	slog.Info("mathpixLambda stage output", "event", ret)

	return ret, nil
}

func getMathpixKeys() (*types.MathpixSecrets, error) {
	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		os.Exit(1)
	}

	svc := secretsmanager.NewFromConfig(awsCfg)

	secretName := types.MATHPIX_SECRETS
	input := &secretsmanager.GetSecretValueInput{SecretId: &secretName}

	result, err := svc.GetSecretValue(context.TODO(), input)
	if err != nil {
		return nil, err
	}

	var mathpixSecrets types.MathpixSecrets

	err = json.Unmarshal([]byte(*result.SecretString), &mathpixSecrets)
	if err != nil {
		return nil, err
	}

	return &mathpixSecrets, nil
}

func init() {
	slog.Debug(">>init")
	defer slog.Debug("<<init")

	awsCfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Failed to load the AWS config", "error", err)
		os.Exit(1)
	}

	store, err := database.NewDynamoDBClient()
	if err != nil {
		slog.Error("Failed to configure the DynamoDB client", "error", err)
		os.Exit(1)
	}

	mathpixKeys, err := getMathpixKeys()
	if err != nil {
		slog.Error("Failed to get the Mathpix keys", "error", err)
		os.Exit(1)
	}

	mathpixAppID := mathpixKeys.AppID
	mathpixAppKey := mathpixKeys.AppKey
	s3Client := s3.NewFromConfig(awsCfg)

	cfg = &mathpixConfig{
		store,
		s3Client,
		mathpixAppID,
		mathpixAppKey,
	}
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(cfg.process)
}
