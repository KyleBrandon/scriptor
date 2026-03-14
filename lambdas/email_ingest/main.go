package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/google/uuid"
	xhtml "golang.org/x/net/html"
)

type handlerConfig struct {
	store           database.DocumentStore
	s3Client        *s3.Client
	sfnClient       *sfn.Client
	httpClient      *http.Client
	stateMachineARN string
}

type parsedEmail struct {
	Sender      string
	Recipient   string
	Subject     string
	HTMLBody    string
	TextBody    string
	MessageID   string
	SentAt      time.Time
	RawS3Key    string
	RawS3Bucket string
}

var (
	initOnce   sync.Once
	cfg        *handlerConfig
	urlPattern = regexp.MustCompile(`https?://[^\s<>"']+`)
)

func loadConfiguration(ctx context.Context) (*handlerConfig, error) {
	cfg = &handlerConfig{}

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("Failed to load the AWS config", "error", err)
		return nil, err
	}

	cfg.store, err = database.NewDocumentStore(ctx)
	if err != nil {
		slog.Error("Failed to configure the DynamoDB client", "error", err)
		return nil, err
	}

	cfg.s3Client = s3.NewFromConfig(awsCfg)
	cfg.sfnClient = sfn.NewFromConfig(awsCfg)
	cfg.httpClient = &http.Client{Timeout: 2 * time.Minute}

	cfg.stateMachineARN = os.Getenv("STATE_MACHINE_ARN")
	if cfg.stateMachineARN == "" {
		return nil, fmt.Errorf("STATE_MACHINE_ARN is required")
	}

	return cfg, nil
}

func initLambda(ctx context.Context) error {
	var err error
	initOnce.Do(func() {
		cfg, err = loadConfiguration(ctx)
	})

	return err
}

func process(ctx context.Context, sqsEvent events.SQSEvent) error {
	if err := initLambda(ctx); err != nil {
		return err
	}

	for _, message := range sqsEvent.Records {
		var s3Event events.S3Event
		if err := json.Unmarshal([]byte(message.Body), &s3Event); err != nil {
			slog.Error("Failed to unmarshal the S3 event notification", "error", err)
			return err
		}

		for _, record := range s3Event.Records {
			if err := cfg.handleS3Record(ctx, message.MessageId, record); err != nil {
				return err
			}
		}
	}

	return nil
}

func (cfg *handlerConfig) handleS3Record(
	ctx context.Context,
	notificationID string,
	record events.S3EventRecord,
) error {
	rawKey, err := url.QueryUnescape(record.S3.Object.Key)
	if err != nil {
		slog.Error("Failed to decode the raw email key", "key", record.S3.Object.Key, "error", err)
		return err
	}

	emailData, err := cfg.readRawEmail(
		ctx,
		record.S3.Bucket.Name,
		rawKey,
	)
	if err != nil {
		return err
	}

	emailData.RawS3Bucket = record.S3.Bucket.Name
	emailData.RawS3Key = rawKey

	downloadURL, err := extractKindleDownloadURL(emailData)
	if err != nil {
		slog.Error("Failed to extract the Kindle download URL", "key", rawKey, "error", err)
		return err
	}

	sourceKey := buildKindleSourceKey(downloadURL)
	if _, err := cfg.store.GetDocumentBySourceKey(ctx, sourceKey); err == nil {
		slog.Warn("Skipping duplicate Kindle email", "sourceKey", sourceKey)
		return nil
	} else if err != nil && err != database.ErrDocumentNotFound {
		return err
	}

	pdfName := path.Base(downloadURL.Path)
	if pdfName == "." || pdfName == "/" || !strings.HasSuffix(strings.ToLower(pdfName), ".pdf") {
		return fmt.Errorf("invalid Kindle PDF path: %s", downloadURL.Path)
	}

	pdfBytes, err := cfg.downloadFile(ctx, downloadURL.String())
	if err != nil {
		slog.Error("Failed to download the Kindle PDF", "url", downloadURL.String(), "error", err)
		return err
	}

	now := time.Now().UTC()
	if emailData.SentAt.IsZero() {
		emailData.SentAt = now
	}

	document := &types.Document{
		ID:                   uuid.New().String(),
		SourceType:           types.DOCUMENT_SOURCE_KINDLE_EMAIL,
		SourceKey:            sourceKey,
		Name:                 pdfName,
		Size:                 int64(len(pdfBytes)),
		CreatedTime:          emailData.SentAt.UTC(),
		ModifiedTime:         now,
		DownloadURL:          downloadURL.String(),
		DownloadURLExpiresAt: parseSignedURLExpiry(downloadURL),
		RawEmailS3Key:        rawKey,
		Sender:               emailData.Sender,
		Recipient:            emailData.Recipient,
	}

	if err := cfg.store.InsertDocument(ctx, document); err != nil {
		return err
	}

	downloadStage, err := cfg.store.StartDocumentStage(
		ctx,
		document.ID,
		types.DOCUMENT_STAGE_DOWNLOAD,
		document.Name,
	)
	if err != nil {
		return err
	}

	if err := cfg.saveDownloadedStage(ctx, document, downloadStage, pdfBytes); err != nil {
		return err
	}

	if err := cfg.store.CompleteDocumentStage(ctx, downloadStage); err != nil {
		return err
	}

	input, err := util.BuildStepInput(
		notificationID,
		document.ID,
		types.DOCUMENT_STAGE_DOWNLOAD,
	)
	if err != nil {
		return err
	}

	_, err = cfg.sfnClient.StartExecution(ctx, &sfn.StartExecutionInput{
		StateMachineArn: aws.String(cfg.stateMachineARN),
		Input:           aws.String(input),
	})
	if err != nil {
		slog.Error("Failed to start the state machine", "documentID", document.ID, "error", err)
		return err
	}

	return nil
}

func (cfg *handlerConfig) readRawEmail(
	ctx context.Context,
	bucketName, key string,
) (*parsedEmail, error) {
	resp, err := cfg.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		slog.Error("Failed to read the raw email from S3", "bucket", bucketName, "key", key, "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	rawMessage, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	message, err := mail.ReadMessage(bytes.NewReader(rawMessage))
	if err != nil {
		return nil, err
	}

	htmlBody, textBody, err := readMessageBody(message.Header, message.Body)
	if err != nil {
		return nil, err
	}

	sentAt, _ := mail.ParseDate(message.Header.Get("Date"))

	return &parsedEmail{
		Sender:    message.Header.Get("From"),
		Recipient: message.Header.Get("To"),
		Subject:   message.Header.Get("Subject"),
		HTMLBody:  htmlBody,
		TextBody:  textBody,
		MessageID: message.Header.Get("Message-ID"),
		SentAt:    sentAt,
	}, nil
}

func readMessageBody(
	header mail.Header,
	body io.Reader,
) (string, string, error) {
	contentType := header.Get("Content-Type")
	if contentType == "" {
		payload, err := decodeBody(body, header.Get("Content-Transfer-Encoding"))
		return "", string(payload), err
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		payload, decodeErr := decodeBody(body, header.Get("Content-Transfer-Encoding"))
		return "", string(payload), decodeErr
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(body, params["boundary"])
		var htmlBody string
		var textBody string

		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", "", err
			}

			partHTML, partText, err := readMessageBody(
				mail.Header(textproto.MIMEHeader(part.Header)),
				part,
			)
			part.Close()
			if err != nil {
				return "", "", err
			}

			if htmlBody == "" && partHTML != "" {
				htmlBody = partHTML
			}
			if textBody == "" && partText != "" {
				textBody = partText
			}
		}

		return htmlBody, textBody, nil
	}

	if mediaType == "message/rfc822" {
		nestedMessage, err := mail.ReadMessage(body)
		if err != nil {
			return "", "", err
		}
		return readMessageBody(nestedMessage.Header, nestedMessage.Body)
	}

	payload, err := decodeBody(body, header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return "", "", err
	}

	switch mediaType {
	case "text/html":
		return string(payload), "", nil
	case "text/plain":
		return "", string(payload), nil
	default:
		return "", "", nil
	}
}

func decodeBody(body io.Reader, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return io.ReadAll(base64.NewDecoder(base64.StdEncoding, body))
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(body))
	default:
		return io.ReadAll(body)
	}
}

func extractKindleDownloadURL(email *parsedEmail) (*url.URL, error) {
	if email.HTMLBody != "" {
		href, err := extractDownloadLinkFromHTML(email.HTMLBody)
		if err == nil && href != "" {
			return resolveDownloadURL(href)
		}
	}

	if email.TextBody != "" {
		href, err := extractDownloadLinkFromText(email.TextBody)
		if err == nil && href != "" {
			return resolveDownloadURL(href)
		}
	}

	return nil, fmt.Errorf("no Kindle PDF link found")
}

func extractDownloadLinkFromHTML(body string) (string, error) {
	document, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return "", err
	}

	var fallback string
	var walk func(*xhtml.Node) string
	walk = func(node *xhtml.Node) string {
		if node.Type == xhtml.ElementNode && node.Data == "a" {
			href := getAttr(node, "href")
			text := strings.ToLower(strings.TrimSpace(nodeText(node)))
			if href != "" {
				href = html.UnescapeString(strings.TrimSpace(href))
			}

			if href != "" && strings.Contains(text, "download pdf") {
				return href
			}

			if fallback == "" && href != "" &&
				(strings.Contains(strings.ToLower(href), "gp/f.html") ||
					strings.HasSuffix(strings.ToLower(href), ".pdf")) {
				fallback = href
			}
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if href := walk(child); href != "" {
				return href
			}
		}

		return ""
	}

	if href := walk(document); href != "" {
		return href, nil
	}

	if fallback != "" {
		return fallback, nil
	}

	return "", fmt.Errorf("no link found in html email body")
}

func extractDownloadLinkFromText(body string) (string, error) {
	for _, candidate := range urlPattern.FindAllString(body, -1) {
		candidate = strings.TrimSpace(strings.Trim(candidate, "<>.,;"))
		lowerCandidate := strings.ToLower(candidate)
		if strings.Contains(lowerCandidate, "gp/f.html") || strings.HasSuffix(lowerCandidate, ".pdf") {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no url found in text body")
}

func resolveDownloadURL(rawURL string) (*url.URL, error) {
	rawURL = html.UnescapeString(strings.TrimSpace(rawURL))

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	if wrappedURL := parsedURL.Query().Get("U"); wrappedURL != "" {
		wrappedURL = html.UnescapeString(wrappedURL)
		if decodedURL, err := url.QueryUnescape(wrappedURL); err == nil {
			wrappedURL = decodedURL
		}

		parsedURL, err = url.Parse(wrappedURL)
		if err != nil {
			return nil, err
		}
	}

	if parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("unexpected download url scheme: %s", parsedURL.Scheme)
	}

	if !strings.Contains(strings.ToLower(parsedURL.Host), "amazonaws.com") {
		return nil, fmt.Errorf("unexpected download host: %s", parsedURL.Host)
	}

	return parsedURL, nil
}

func buildKindleSourceKey(downloadURL *url.URL) string {
	return fmt.Sprintf(
		"%s:%s%s",
		types.DOCUMENT_SOURCE_KINDLE_EMAIL,
		strings.ToLower(downloadURL.Host),
		downloadURL.EscapedPath(),
	)
}

func parseSignedURLExpiry(downloadURL *url.URL) time.Time {
	query := downloadURL.Query()
	dateValue := query.Get("X-Amz-Date")
	expiryValue := query.Get("X-Amz-Expires")
	if dateValue == "" || expiryValue == "" {
		return time.Time{}
	}

	signedAt, err := time.Parse("20060102T150405Z", dateValue)
	if err != nil {
		return time.Time{}
	}

	expiresIn, err := time.ParseDuration(expiryValue + "s")
	if err != nil {
		return time.Time{}
	}

	return signedAt.Add(expiresIn).UTC()
}

func (cfg *handlerConfig) downloadFile(
	ctx context.Context,
	fileURL string,
) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, err
	}

	response, err := cfg.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected download status: %s", response.Status)
	}

	return io.ReadAll(response.Body)
}

func (cfg *handlerConfig) saveDownloadedStage(
	ctx context.Context,
	document *types.Document,
	stage *types.DocumentProcessingStage,
	pdfBytes []byte,
) error {
	documentName := util.GetNamePart(document.Name)
	stage.StageFileName = fmt.Sprintf(
		"%s-%d.pdf",
		documentName,
		time.Now().UTC().Unix(),
	)
	stage.S3Key = fmt.Sprintf("%s/%s", stage.Stage, stage.StageFileName)

	_, err := cfg.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(types.S3_BUCKET_NAME),
		Key:           aws.String(stage.S3Key),
		Body:          bytes.NewReader(pdfBytes),
		ContentType:   aws.String("application/pdf"),
		ContentLength: aws.Int64(int64(len(pdfBytes))),
	})
	if err != nil {
		slog.Error("Failed to save the downloaded Kindle PDF", "documentID", document.ID, "error", err)
		return err
	}

	return nil
}

func getAttr(node *xhtml.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}

	return ""
}

func nodeText(node *xhtml.Node) string {
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current.Type == xhtml.TextNode {
			builder.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)

	return builder.String()
}

func main() {
	lambda.Start(process)
}
