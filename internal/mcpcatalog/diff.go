package mcpcatalog

import (
	"reflect"
	"sort"
)

// Delta describes the difference between two catalogs.
type Delta struct {
	Added   []*Entry    // in remote, not in local
	Removed []*Entry    // in local, not in remote
	Changed []DeltaPair // id matches, content differs
}

// DeltaPair couples the local and remote versions of a changed entry.
type DeltaPair struct {
	Local  *Entry
	Remote *Entry
}

// IsEmpty reports whether the two catalogs are equivalent.
func (d Delta) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// Diff compares two catalogs. Equality is reflect.DeepEqual on the decoded
// *Entry, so unchanged entries do not show up in Changed.
//
// Either argument may be nil — a nil catalog is treated as empty.
func Diff(local, remote *Catalog) Delta {
	out := Delta{}
	localIDs := map[string]*Entry{}
	remoteIDs := map[string]*Entry{}
	if local != nil {
		localIDs = local.Entries
	}
	if remote != nil {
		remoteIDs = remote.Entries
	}

	// Sort iteration order for deterministic results.
	for _, id := range sortedKeys(remoteIDs) {
		r := remoteIDs[id]
		l, ok := localIDs[id]
		if !ok {
			out.Added = append(out.Added, r)
			continue
		}
		if !reflect.DeepEqual(l, r) {
			out.Changed = append(out.Changed, DeltaPair{Local: l, Remote: r})
		}
	}
	for _, id := range sortedKeys(localIDs) {
		if _, ok := remoteIDs[id]; !ok {
			out.Removed = append(out.Removed, localIDs[id])
		}
	}
	return out
}

func sortedKeys(m map[string]*Entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
