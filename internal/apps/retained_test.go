package apps

import "testing"

// `apps delete` must say what survived the app (DIB-959): a database made
// with `--deployment` is the organization's and is not dropped with the app,
// and a user who is not told will assume it was.
func TestRetainedResourcesNotice(t *testing.T) {
	if got := RetainedResourcesNotice(nil); got != "" {
		t.Errorf("nil: %q", got)
	}
	if got := RetainedResourcesNotice(&DeleteResponse{Status: "success"}); got != "" {
		t.Errorf("nothing retained: %q", got)
	}
	got := RetainedResourcesNotice(&DeleteResponse{RetainedDatabases: []string{"orders"}, RetainedBuckets: []string{"uploads", "exports"}})
	want := "Database 'orders' belongs to your organization and still exists — delete with `dibbla db delete <name>`.\n" +
		"Buckets 'uploads', 'exports' belong to your organization and still exist — delete with `dibbla storage delete <name>`."
	if got != want {
		t.Errorf("notice:\n%s\nwant:\n%s", got, want)
	}
}
