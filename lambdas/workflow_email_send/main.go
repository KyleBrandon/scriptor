package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/KyleBrandon/scriptor/lambdas/util"
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sesTypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
)

const maxRawEmailSizeBytes = 40 * 1024 * 1024

type (
	handlerConfig struct {
		store       database.DocumentStore
		s3Client    *s3.Client
		emailClient *ses.Client
		emailConfig *types.PostUploadEmailConfig
	}

	emailAttachment struct {
		FileName    string
		ContentType string
		Content     []byte
	}
)

var (
	initOnce sync.Once
	cfg      *handlerConfig
)

func loadConfiguration(ctx context.Context) (*handlerConfig, error) {
	cfg = &handlerConfig{}

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("failed to load the AWS config", "error", err)
		return nil, err
	}

	cfg.store, err = database.NewDocumentStore(ctx)
	if err != nil {
		slog.Error("Failed to configure the DynamoDB client", "error", err)
		return nil, err
	}

	cfg.s3Client = s3.NewFromConfig(awsCfg)
	cfg.emailClient = ses.NewFromConfig(awsCfg)

	cfg.emailConfig, err = util.LoadPostUploadEmailConfig(ctx, awsCfg)
	if err != nil {
		slog.Error(
			"Failed to load the post-upload email config",
			"error",
			err,
		)
		return nil, err
	}

	return cfg, nil
}

func initLambda(ctx context.Context) error {
	var err error
	initOnce.Do(func() {
		slog.Debug(">>initLambda")
		defer slog.Debug("<<initLambda")

		cfg, err = loadConfiguration(ctx)
	})

	return err
}

func (cfg *handlerConfig) getStageBytes(
	ctx context.Context,
	docStage *types.DocumentProcessingStage,
) ([]byte, error) {
	resp, err := cfg.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(types.S3_BUCKET_NAME),
		Key:    aws.String(docStage.S3Key),
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
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

	uploadStage, err := cfg.store.GetDocumentStage(ctx, event.DocumentID, event.Stage)
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

	openAIStage, err := cfg.store.GetDocumentStage(
		ctx,
		event.DocumentID,
		types.DOCUMENT_STAGE_OPENAI,
	)
	if err != nil {
		slog.Error(
			"Failed to get the openai stage information",
			"id",
			event.DocumentID,
			"error",
			err,
		)
		return ret, err
	}

	document, err := cfg.store.GetDocument(ctx, event.DocumentID)
	if err != nil {
		slog.Error(
			"Failed to get the document information",
			"id",
			event.DocumentID,
			"error",
			err,
		)
		return ret, err
	}

	emailStage, err := cfg.store.StartDocumentStage(
		ctx,
		event.DocumentID,
		types.DOCUMENT_STAGE_EMAIL,
		document.Name,
	)
	if err != nil {
		slog.Error(
			"Failed to start the email document processing stage",
			"id",
			event.DocumentID,
			"error",
			err,
		)
		return ret, err
	}

	if !cfg.emailConfig.Enabled {
		slog.Info(
			"Skipping post-upload email because it is disabled",
			"id",
			event.DocumentID,
		)
	} else {
		pdfBytes, err := cfg.getStageBytes(ctx, downloadedStage)
		if err != nil {
			slog.Error(
				"Failed to get the original PDF from S3",
				"id",
				event.DocumentID,
				"key",
				downloadedStage.S3Key,
				"error",
				err,
			)
			return ret, err
		}

		markdownBytes, err := cfg.getStageBytes(ctx, openAIStage)
		if err != nil {
			slog.Error(
				"Failed to get the final Markdown from S3",
				"id",
				event.DocumentID,
				"key",
				openAIStage.S3Key,
				"error",
				err,
			)
			return ret, err
		}

		baseName := util.GetNamePart(document.Name)
		completedAt := uploadStage.CompletedAt
		if completedAt.IsZero() {
			completedAt = time.Now().UTC()
		}

		subject := buildEmailSubject(cfg.emailConfig.SubjectPrefix, baseName)
		body := buildEmailBody(document, completedAt)

		rawEmail, err := buildRawEmail(
			cfg.emailConfig,
			subject,
			body,
			[]emailAttachment{
				{
					FileName:    fmt.Sprintf("%s.pdf", baseName),
					ContentType: "application/pdf",
					Content:     pdfBytes,
				},
				{
					FileName:    fmt.Sprintf("%s.md", baseName),
					ContentType: "text/markdown; charset=utf-8",
					Content:     markdownBytes,
				},
			},
		)
		if err != nil {
			slog.Error(
				"Failed to build the outbound email",
				"id",
				event.DocumentID,
				"error",
				err,
			)
			return ret, err
		}

		resp, err := cfg.emailClient.SendRawEmail(ctx, &ses.SendRawEmailInput{
			RawMessage: &sesTypes.RawMessage{
				Data: rawEmail,
			},
			Source:       aws.String(cfg.emailConfig.FromAddress),
			Destinations: cfg.emailConfig.ToAddresses,
		})
		if err != nil {
			slog.Error(
				"Failed to send the post-upload email",
				"id",
				event.DocumentID,
				"recipientCount",
				len(cfg.emailConfig.ToAddresses),
				"error",
				err,
			)
			return ret, err
		}

		slog.Info(
			"Sent the post-upload email",
			"id",
			event.DocumentID,
			"messageID",
			aws.ToString(resp.MessageId),
			"recipientCount",
			len(cfg.emailConfig.ToAddresses),
		)
	}

	if err := cfg.store.CompleteDocumentStage(ctx, emailStage); err != nil {
		slog.Error(
			"Failed to update the email processing stage as complete",
			"id",
			event.DocumentID,
			"error",
			err,
		)
		return ret, err
	}

	ret.NotificationID = event.NotificationID
	ret.DocumentID = event.DocumentID
	ret.Stage = types.DOCUMENT_STAGE_EMAIL

	return ret, nil
}

func buildEmailSubject(subjectPrefix, documentName string) string {
	documentName = strings.TrimSpace(documentName)
	if documentName == "" {
		documentName = "Document"
	}

	subjectPrefix = strings.TrimSpace(subjectPrefix)
	if subjectPrefix == "" {
		return documentName
	}

	return fmt.Sprintf("%s %s", subjectPrefix, documentName)
}

func buildEmailBody(document *types.Document, completedAt time.Time) string {
	return fmt.Sprintf(
		"Scriptor finished processing a document.\n\nDocument: %s\nSource: %s\nCompleted: %s\n\nAttachments:\n- Original PDF\n- Final Markdown\n",
		document.Name,
		document.SourceType,
		completedAt.UTC().Format(time.RFC3339),
	)
}

func buildRawEmail(
	emailConfig *types.PostUploadEmailConfig,
	subject, body string,
	attachments []emailAttachment,
) ([]byte, error) {
	var buffer bytes.Buffer

	writer := multipart.NewWriter(&buffer)

	fmt.Fprintf(&buffer, "From: %s\r\n", emailConfig.FromAddress)
	fmt.Fprintf(&buffer, "To: %s\r\n", strings.Join(emailConfig.ToAddresses, ", "))
	if len(emailConfig.ReplyTo) > 0 {
		fmt.Fprintf(
			&buffer,
			"Reply-To: %s\r\n",
			strings.Join(emailConfig.ReplyTo, ", "),
		)
	}
	fmt.Fprintf(
		&buffer,
		"Subject: %s\r\n",
		mime.QEncoding.Encode("utf-8", subject),
	)
	fmt.Fprintf(&buffer, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(
		&buffer,
		"Content-Type: multipart/mixed; boundary=%q\r\n",
		writer.Boundary(),
	)
	fmt.Fprintf(&buffer, "\r\n")

	textPart, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"7bit"},
	})
	if err != nil {
		return nil, err
	}

	if _, err := io.WriteString(textPart, body); err != nil {
		return nil, err
	}

	for _, attachment := range attachments {
		fileName := sanitizeAttachmentFileName(attachment.FileName)

		part, err := writer.CreatePart(textproto.MIMEHeader{
			"Content-Type": {
				fmt.Sprintf("%s; name=%q", attachment.ContentType, fileName),
			},
			"Content-Description":       {fileName},
			"Content-Disposition":       {fmt.Sprintf(`attachment; filename="%s"`, fileName)},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return nil, err
		}

		if err := writeBase64Chunks(part, attachment.Content); err != nil {
			return nil, err
		}
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}

	if buffer.Len() > maxRawEmailSizeBytes {
		return nil, fmt.Errorf(
			"raw email size %d exceeds SES maximum of %d bytes",
			buffer.Len(),
			maxRawEmailSizeBytes,
		)
	}

	return buffer.Bytes(), nil
}

func writeBase64Chunks(writer io.Writer, data []byte) error {
	encoded := base64.StdEncoding.EncodeToString(data)

	for len(encoded) > 76 {
		if _, err := io.WriteString(writer, encoded[:76]+"\r\n"); err != nil {
			return err
		}
		encoded = encoded[76:]
	}

	if encoded == "" {
		return nil
	}

	_, err := io.WriteString(writer, encoded+"\r\n")
	return err
}

func sanitizeAttachmentFileName(fileName string) string {
	replacer := strings.NewReplacer(`"`, "", "\r", "", "\n", "")
	return replacer.Replace(fileName)
}

func main() {
	slog.Debug(">>main")
	defer slog.Debug("<<main")

	lambda.Start(process)
}
