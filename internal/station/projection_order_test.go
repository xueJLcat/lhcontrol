package station

import (
	"math/rand"
	"sort"
	"testing"
)

// TestStationValuesLessIsStrictWeakOrder samples triples of station keys and
// verifies the comparator is a strict weak order (antisymmetric, transitive,
// equivalence classes consistent). A violation would make sort.Slice produce
// unstable fleet ordering, which the hardware smoke script asserts against.
func TestStationValuesLessIsStrictWeakOrder(t *testing.T) {
	type key struct {
		channel int
		name    string
		address string
	}
	keys := make([]key, 0, 144)
	for _, channel := range []int{0, -1, 1, 2, 16, 17} {
		for _, name := range []string{"", "a", "A", "b", "LHB-Zz", "LHB-zZ"} {
			for _, address := range []string{"", "AA:AA", "aa:aa", "BB:BB"} {
				keys = append(keys, key{channel, name, address})
			}
		}
	}
	less := func(a, b key) bool {
		return stationValuesLess(a.channel, a.name, a.address, b.channel, b.name, b.address)
	}
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200000; trial++ {
		x := keys[rng.Intn(len(keys))]
		y := keys[rng.Intn(len(keys))]
		z := keys[rng.Intn(len(keys))]
		if less(x, y) && less(y, x) {
			t.Fatalf("antisymmetry violated for %+v and %+v", x, y)
		}
		if less(x, y) && less(y, z) && !less(x, z) {
			t.Fatalf("transitivity violated: %+v < %+v < %+v but not x < z", x, y, z)
		}
		if !less(x, y) && !less(y, x) && !less(y, z) && !less(z, y) && less(x, z) && less(z, x) {
			t.Fatalf("equivalence class inconsistency: %+v %+v %+v", x, y, z)
		}
	}
}

// TestProjectionSortStableAcrossShuffledInputs sorts the same station set from
// many shuffled orders and asserts an identical result order every time, so a
// re-projected fleet never reshuffles visually unchanged stations.
func TestProjectionSortStableAcrossShuffledInputs(t *testing.T) {
	type key struct {
		channel int
		name    string
		address string
	}
	base := []key{
		{0, "LHB-B", "BB:BB"},
		{-1, "LHB-A", "AA:AA"},
		{3, "LHB-C", "CC:CC"},
		{3, "lhb-c", "cc:dd"},
		{16, "LHB-Z", "ZZ:ZZ"},
		{0, "LHB-A", "aa:bb"},
	}
	rng := rand.New(rand.NewSource(7))
	var reference []key
	for trial := 0; trial < 200; trial++ {
		input := make([]key, len(base))
		perm := rng.Perm(len(base))
		for i, p := range perm {
			input[i] = base[p]
		}
		sort.Slice(input, func(i, j int) bool {
			return stationValuesLess(input[i].channel, input[i].name, input[i].address, input[j].channel, input[j].name, input[j].address)
		})
		if reference == nil {
			reference = input
			continue
		}
		for i := range reference {
			if reference[i] != input[i] {
				t.Fatalf("sort order differs across shuffled inputs at %d: %+v vs %+v", i, reference, input)
			}
		}
	}
}
