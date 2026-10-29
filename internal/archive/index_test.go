package archive

import "testing"

// The car's RecentClips/thumb.png has no time in its name. It goes with
// the newest drive instead of becoming a drive of its own without a date.
func TestRecentThumbJoinsNewestDrive(t *testing.T) {
	ix := NewIndex(t.TempDir(), nil)
	for _, rel := range []string{
		"RecentClips/2026-10-28/2026-10-28_18-02-00-front.mp4",
		"RecentClips/2026-10-29/2026-10-29_08-36-31-front.mp4",
		"RecentClips/2026-10-29/2026-10-29_08-37-31-front.mp4",
		"RecentClips/thumb.png",
	} {
		ix.Add(rel, 100)
	}
	groups := ix.Groups()
	if len(groups) != 2 {
		t.Fatalf("%d groups, want 2 drives: %+v", len(groups), groups)
	}
	newest := groups[0]
	if newest.ID != "RecentClips/2026-10-29_08-36-31" || newest.Thumb != "RecentClips/thumb.png" {
		t.Errorf("newest drive %q has thumb %q", newest.ID, newest.Thumb)
	}
	if groups[1].Thumb != "" {
		t.Errorf("older drive got the thumb too")
	}
}
