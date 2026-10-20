package storage

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	gcs "cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/api/option"
)

func TestGCSRecursiveListingOmitsDelimiterAndRetainsPrefix(t *testing.T) {
	ctx := context.Background()
	client, err := gcs.NewClient(ctx, option.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("delimiter") != "" || r.URL.Query().Get("prefix") != "team/a/" || r.URL.Query().Get("pageToken") != "cursor" {
			t.Fatalf("bad recursive query: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"items":[{"name":"team/a/nested/config.yaml","size":"5","generation":"1"}],"nextPageToken":"next"}`))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	p := &GCS{Client: client}
	out, err := p.ListRecursive(ctx, "bucket", "team/a/", "cursor", 1)
	if err != nil || len(out.Objects) != 1 || out.Objects[0].Key != "team/a/nested/config.yaml" || out.Next != "next" {
		t.Fatal(out, err)
	}
}

func TestS3RecursiveListingOmitsDelimiterAndRetainsPrefix(t *testing.T) {
	client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""), HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("delimiter") != "" || r.URL.Query().Get("prefix") != "team/a/" || r.URL.Query().Get("continuation-token") != "cursor" {
			t.Fatalf("bad recursive query: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/xml"}}, Body: io.NopCloser(strings.NewReader(`<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>next</NextContinuationToken><Contents><Key>team/a/nested/config.yaml</Key><Size>5</Size><ETag>v1</ETag></Contents></ListBucketResult>`))}, nil
	})}}, func(o *s3.Options) { o.BaseEndpoint = aws.String("https://s3.example.com"); o.UsePathStyle = true })
	p := &S3{Client: client}
	out, err := p.ListRecursive(context.Background(), "bucket", "team/a/", "cursor", 1)
	if err != nil || len(out.Objects) != 1 || out.Objects[0].Key != "team/a/nested/config.yaml" || out.Next != "next" {
		t.Fatal(out, err)
	}
}
