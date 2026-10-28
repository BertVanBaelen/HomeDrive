package files

import (
	"slices"
	"testing"
	"time"
)

func TestSortNodes(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }
	nodes := []Node{
		{Name: "IMG 10.jpg", Kind: "file", Size: 5, ModifiedAt: day(3)},
		{Name: "img 2.jpg", Kind: "file", Size: 50, ModifiedAt: day(1)},
		{Name: "Zebra", Kind: "folder", ModifiedAt: day(2)},
		{Name: "apple.txt", Kind: "file", Size: 7, ModifiedAt: day(2)},
		{Name: "Archive", Kind: "folder", ModifiedAt: day(5)},
	}
	names := func() []string {
		var out []string
		for _, n := range nodes {
			out = append(out, n.Name)
		}
		return out
	}
	SortNodes(nodes, SortName, false)
	if want := []string{"Archive", "Zebra", "apple.txt", "img 2.jpg", "IMG 10.jpg"}; !slices.Equal(names(), want) {
		t.Errorf("by name: %v", names())
	}
	SortNodes(nodes, SortSize, true)
	if want := []string{"Zebra", "Archive", "img 2.jpg", "apple.txt", "IMG 10.jpg"}; !slices.Equal(names(), want) {
		t.Errorf("by size desc: %v", names())
	}
	SortNodes(nodes, SortModified, false)
	if want := []string{"Zebra", "Archive", "img 2.jpg", "apple.txt", "IMG 10.jpg"}; !slices.Equal(names(), want) {
		t.Errorf("by modified: %v", names())
	}
}

func TestValidateName(t *testing.T) {
	for name, want := range map[string]string{
		"  report.pdf ": "report.pdf",
		"a:b*c?.txt":    "a:b*c?.txt", // fine: names are never paths
		"..hidden":      "..hidden",
	} {
		if got, err := ValidateName(name); got != want || err != nil {
			t.Errorf("ValidateName(%q) = %q, %v", name, got, err)
		}
	}
	for _, bad := range []string{"", " ", ".", "..", "a/b", `a\b`, "tab\there", "\xff", string(make([]byte, 256))} {
		if _, err := ValidateName(bad); err != ErrInvalidName {
			t.Errorf("ValidateName(%q) accepted", bad)
		}
	}
}
