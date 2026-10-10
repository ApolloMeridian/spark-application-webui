package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMultiBucketBootstrapBothProviders(t *testing.T) {
	for _, provider := range []string{"s3", "gcs"} {
		c := Config{Connections: []Connection{{ID: "store", Provider: provider, Buckets: []string{"spark", "ubuntu"}}}, Bootstrap: []Bootstrap{{Username: "admin", Tenant: "admin", Connection: "store", AllBuckets: true, Actions: Actions}}}
		x, err := c.ExpandedBootstrap()
		if err != nil || len(x) != 2 || x[0].Bucket != "spark" || x[1].Bucket != "ubuntu" || x[0].AllBuckets {
			t.Fatal(x, err)
		}
		c.Bootstrap[0].AllBuckets = false
		c.Bootstrap[0].Bucket = "spark"
		x, err = c.ExpandedBootstrap()
		if err != nil || len(x) != 1 {
			t.Fatal("explicit bucket expanded unexpectedly", x, err)
		}
		c.Bootstrap[0].Bucket = "unconfigured"
		if _, err = c.ExpandedBootstrap(); err == nil {
			t.Fatal("accepted unconfigured bucket")
		}
	}
}
func TestDirectoriesHaveNoModificationTime(t *testing.T) {
	raw, err := json.Marshal(Object{Key: "folder/", Directory: true})
	if err != nil || strings.Contains(string(raw), "modified") {
		t.Fatal(string(raw), err)
	}
}
