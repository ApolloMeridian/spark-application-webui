package storage

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3 struct {
	Client      *s3.Client
	Chunk       int64
	Conditional bool
}

func NewS3(c Connection, chunk int64) (*S3, error) {
	access, e := os.ReadFile(c.S3.AccessKeyFile)
	if e != nil {
		return nil, e
	}
	secret, e := os.ReadFile(c.S3.SecretKeyFile)
	if e != nil {
		return nil, e
	}
	if strings.TrimSpace(string(access)) == "" || strings.TrimSpace(string(secret)) == "" {
		return nil, fmt.Errorf("empty S3 credentials")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 16
	transport.ResponseHeaderTimeout = 30 * time.Second
	if c.S3.CAFile != "" {
		pem, e := os.ReadFile(c.S3.CAFile)
		if e != nil {
			return nil, e
		}
		roots, e := x509.SystemCertPool()
		if e != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("invalid S3 CA")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	cfg := aws.Config{Region: c.S3.Region, Credentials: credentials.NewStaticCredentialsProvider(strings.TrimSpace(string(access)), strings.TrimSpace(string(secret)), ""), HTTPClient: &http.Client{Transport: transport}, RetryMaxAttempts: 2}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(c.S3.Endpoint)
		o.UsePathStyle = c.S3.ForcePathStyle
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &S3{client, chunk, c.S3.ConditionalWrites}, nil
}
func (s *S3) List(ctx context.Context, b, p, token string, n int) (Page, error) {
	x, e := s.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(b), Prefix: aws.String(p), Delimiter: aws.String("/"), ContinuationToken: optional(token), MaxKeys: aws.Int32(int32(n))})
	if e != nil {
		return Page{}, e
	}
	out := Page{Objects: []Object{}, Next: aws.ToString(x.NextContinuationToken)}
	for _, v := range x.CommonPrefixes {
		out.Objects = append(out.Objects, Object{Key: aws.ToString(v.Prefix), Directory: true})
	}
	for _, v := range x.Contents {
		if aws.ToString(v.Key) != p {
			out.Objects = append(out.Objects, Object{Key: aws.ToString(v.Key), Size: aws.ToInt64(v.Size), Version: strings.Trim(aws.ToString(v.ETag), "\""), Modified: aws.ToTime(v.LastModified)})
		}
	}
	return out, nil
}
func (s *S3) Head(ctx context.Context, b, k string) (Object, error) {
	x, e := s.Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(b), Key: aws.String(k)})
	if e != nil {
		return Object{}, e
	}
	return Object{Key: k, Size: aws.ToInt64(x.ContentLength), Version: strings.Trim(aws.ToString(x.ETag), "\""), Modified: aws.ToTime(x.LastModified)}, nil
}
func (s *S3) Open(ctx context.Context, b, k, v string) (io.ReadCloser, error) {
	x, e := s.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b), Key: aws.String(k), IfMatch: optional(v)})
	if e != nil {
		return nil, e
	}
	return x.Body, nil
}
func (s *S3) Put(ctx context.Context, b, k string, r io.Reader, size int64, v string) (Object, error) {
	if !s.Conditional {
		return Object{}, fail(422, "conditional_writes_not_supported")
	}
	input := &s3.PutObjectInput{Bucket: aws.String(b), Key: aws.String(k), Body: r, ContentLength: aws.Int64(size), ContentType: aws.String("application/octet-stream")}
	if v == "" {
		input.IfNoneMatch = aws.String("*")
	} else {
		input.IfMatch = aws.String(v)
	}
	x, e := s.Client.PutObject(ctx, input)
	if e != nil {
		return Object{}, e
	}
	return Object{Key: k, Size: size, Version: strings.Trim(aws.ToString(x.ETag), "\""), Modified: time.Now().UTC()}, nil
}
func (s *S3) Delete(ctx context.Context, b, k, v string) error {
	current, err := s.Head(ctx, b, k)
	if err != nil {
		return err
	}
	if current.Version != v {
		return fail(409, "version_conflict")
	}
	// Older S3-compatible servers ignore DeleteObject If-Match. The HEAD guard
	// detects stale UI selections but cannot make deletion atomic on those servers.
	_, e := s.Client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b), Key: aws.String(k), IfMatch: optional(v)})
	return e
}
func (s *S3) Start(ctx context.Context, b, k string, size int64, v string) (Upload, error) {
	if !s.Conditional {
		return Upload{}, fail(422, "conditional_writes_not_supported")
	}
	marker := newID()
	x, e := s.Client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(b), Key: aws.String(k), ContentType: aws.String("application/octet-stream"), Metadata: map[string]string{"spark-console-upload-id": marker}})
	if e != nil {
		return Upload{}, e
	}
	return Upload{ID: aws.ToString(x.UploadId), Size: size, Version: v, Marker: marker, Parts: []Part{}}, nil
}
func (s *S3) Part(ctx context.Context, b, k string, u Upload, n int32, r io.Reader, size int64) (Upload, error) {
	x, e := s.Client.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String(b), Key: aws.String(k), UploadId: aws.String(u.ID), PartNumber: aws.Int32(n), Body: r, ContentLength: aws.Int64(size)})
	if e != nil {
		return u, e
	}
	_ = x
	return s.Status(ctx, b, k, u)
}
func (s *S3) Status(ctx context.Context, b, k string, u Upload) (Upload, error) {
	u.Parts = []Part{}
	u.Offset = 0
	var marker *string
	for {
		x, e := s.Client.ListParts(ctx, &s3.ListPartsInput{Bucket: aws.String(b), Key: aws.String(k), UploadId: aws.String(u.ID), PartNumberMarker: marker})
		if e != nil {
			head, he := s.Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(b), Key: aws.String(k)})
			if he == nil && aws.ToInt64(head.ContentLength) == u.Size && u.Marker != "" && head.Metadata["spark-console-upload-id"] == u.Marker {
				u.Complete = true
				u.Offset = u.Size
				u.FinalVersion = strings.Trim(aws.ToString(head.ETag), "\"")
				return u, nil
			}
			return u, e
		}
		for _, p := range x.Parts {
			u.Parts = append(u.Parts, Part{aws.ToInt32(p.PartNumber), aws.ToString(p.ETag), aws.ToInt64(p.Size)})
			u.Offset += aws.ToInt64(p.Size)
		}
		if !aws.ToBool(x.IsTruncated) {
			break
		}
		marker = x.NextPartNumberMarker
	}
	return u, nil
}
func (s *S3) Complete(ctx context.Context, b, k string, u Upload) (Object, error) {
	latest, e := s.Status(ctx, b, k, u)
	if e != nil {
		return Object{}, e
	}
	if latest.Complete {
		return Object{Key: k, Size: u.Size, Version: latest.FinalVersion}, nil
	}
	if latest.Offset != u.Size || len(latest.Parts) == 0 {
		return Object{}, fail(400, "upload_incomplete")
	}
	parts := []types.CompletedPart{}
	for i, p := range latest.Parts {
		if p.Number != int32(i+1) {
			return Object{}, fail(400, "upload_incomplete")
		}
		parts = append(parts, types.CompletedPart{PartNumber: aws.Int32(p.Number), ETag: aws.String(p.ETag)})
	}
	input := &s3.CompleteMultipartUploadInput{Bucket: aws.String(b), Key: aws.String(k), UploadId: aws.String(u.ID), MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}}
	if u.Version == "" {
		input.IfNoneMatch = aws.String("*")
	} else {
		input.IfMatch = aws.String(u.Version)
	}
	x, e := s.Client.CompleteMultipartUpload(ctx, input)
	if e != nil {
		return Object{}, e
	}
	return Object{Key: k, Size: u.Size, Version: strings.Trim(aws.ToString(x.ETag), "\""), Modified: time.Now().UTC()}, nil
}
func (s *S3) Abort(ctx context.Context, b, k string, u Upload) error {
	_, e := s.Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(b), Key: aws.String(k), UploadId: aws.String(u.ID)})
	return e
}
func (s *S3) Copy(ctx context.Context, b, k, destBucket, destKey, v string) (out Object, err error) {
	head, err := s.Head(ctx, b, k)
	if err != nil {
		return out, err
	}
	if head.Version != v {
		return out, fail(409, "version_conflict")
	}
	if head.Size == 0 {
		return s.Put(ctx, destBucket, destKey, strings.NewReader(""), 0, "")
	}
	u, err := s.Start(ctx, destBucket, destKey, head.Size, "")
	if err != nil {
		return out, err
	}
	defer func() {
		if err != nil {
			c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = s.Abort(c, destBucket, destKey, u)
		}
	}()
	// Multipart copy makes the destination creation atomic via If-None-Match.
	copySource := url.PathEscape(b + "/" + k)
	// Server-side ranges do not consume local buffers; enlarge them for large objects.
	chunk := s.Chunk
	if minimum := (head.Size + 9998) / 9999; minimum > chunk {
		chunk = minimum
	}
	for offset, n := int64(0), int32(1); offset < head.Size; n++ {
		end := offset + chunk - 1
		if end >= head.Size {
			end = head.Size - 1
		}
		_, err = s.Client.UploadPartCopy(ctx, &s3.UploadPartCopyInput{Bucket: aws.String(destBucket), Key: aws.String(destKey), UploadId: aws.String(u.ID), PartNumber: aws.Int32(n), CopySource: aws.String(copySource), CopySourceIfMatch: aws.String(head.Version), CopySourceRange: aws.String("bytes=" + strconv.FormatInt(offset, 10) + "-" + strconv.FormatInt(end, 10))})
		if err != nil {
			return out, err
		}
		offset = end + 1
	}
	return s.Complete(ctx, destBucket, destKey, u)
}
func optional(x string) *string {
	if x == "" {
		return nil
	}
	return aws.String(x)
}
