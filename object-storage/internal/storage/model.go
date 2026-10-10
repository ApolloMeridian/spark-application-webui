package storage

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

const TextLimit int64 = 1048576

var Actions = []string{"list", "read", "download", "upload", "edit", "delete", "copy-source", "copy-destination"}

type Identity struct {
	ID       string `json:"id"`
	Tenant   string `json:"tenant"`
	Role     string `json:"role"`
	Username string `json:"username"`
}
type Grant struct {
	ID         string   `json:"id"`
	UserID     string   `json:"userId"`
	Tenant     string   `json:"tenantId"`
	Connection string   `json:"connection"`
	Bucket     string   `json:"bucket"`
	Prefix     string   `json:"prefix"`
	Actions    []string `json:"actions"`
}
type Object struct {
	Key       string    `json:"key"`
	Size      int64     `json:"size"`
	Version   string    `json:"version"`
	Modified  time.Time `json:"modified"`
	Directory bool      `json:"directory"`
}
type Page struct {
	Objects []Object `json:"objects"`
	Next    string   `json:"next"`
}

// Common prefixes are virtual directories and have no modification timestamp.
func (o Object) MarshalJSON() ([]byte, error) {
	type plain Object
	var modified *time.Time
	if !o.Directory && !o.Modified.IsZero() {
		modified = &o.Modified
	}
	return json.Marshal(struct {
		plain
		Modified *time.Time `json:"modified,omitempty"`
	}{plain(o), modified})
}

type Part struct {
	Number int32  `json:"number"`
	ETag   string `json:"etag"`
	Size   int64  `json:"size"`
}
type Upload struct {
	Marker       string `json:"marker"`
	FinalVersion string `json:"finalVersion"`
	ID           string `json:"id"`
	Size         int64  `json:"size"`
	Version      string `json:"version"`
	Offset       int64  `json:"offset"`
	Parts        []Part `json:"parts"`
	Complete     bool   `json:"complete"`
}

// Providers receive already-authorized raw object keys, never HTTP paths.
type Provider interface {
	List(context.Context, string, string, string, int) (Page, error)
	Head(context.Context, string, string) (Object, error)
	Open(context.Context, string, string, string) (io.ReadCloser, error)
	Put(context.Context, string, string, io.Reader, int64, string) (Object, error)
	Delete(context.Context, string, string, string) error
	Copy(context.Context, string, string, string, string, string) (Object, error)
	Start(context.Context, string, string, int64, string) (Upload, error)
	Part(context.Context, string, string, Upload, int32, io.Reader, int64) (Upload, error)
	Status(context.Context, string, string, Upload) (Upload, error)
	Complete(context.Context, string, string, Upload) (Object, error)
	Abort(context.Context, string, string, Upload) error
}
type Failure struct {
	Status int
	Code   string
}

func (e *Failure) Error() string         { return e.Code }
func fail(status int, code string) error { return &Failure{status, code} }
