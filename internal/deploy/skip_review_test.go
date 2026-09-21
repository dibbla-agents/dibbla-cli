package deploy

import "testing"

// The server gates every deploy on REVIEW.md and the handbook (DIB-966), so
// --skip-review must reach it as skip_review=true — and only when set, so an
// ordinary deploy is gated on the server too.
func TestRunSkipReviewTravelsWithTheArchive(t *testing.T) {
	for _, skip := range []bool{true, false} {
		dir := t.TempDir()
		writeFile(t, dir, "Dockerfile", "FROM scratch\n")
		f := newFakeDeployServer(t)
		if _, err := Run(Options{APIURL: f.srv.URL, APIToken: "tok", Path: dir, Alias: "shop", SkipReview: skip}, nil); err != nil {
			t.Fatalf("run: %v", err)
		}
		got, ok := f.formVals["skip_review"]
		if skip && got != "true" {
			t.Errorf("skip_review: want true, got %q", got)
		}
		if !skip && ok {
			t.Errorf("skip_review should be omitted when the flag is not set, got %q", got)
		}
	}
}
