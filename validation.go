package main

// Keep every observation of one star on the same side of the split. A star in
// three overlapping tiles must not train the model via A-B and validate it via B-C.
// Coordinates come from the same per-tile detection lists, so shared observations
// have identical full-resolution coordinates.
func splitMatchTracks(ms []match) (train, validation []match) {
	type observation struct {
		tile     int
		position [2]float64
	}
	nodes := map[observation]int{}
	var parent []int
	node := func(p observation) int {
		if i, ok := nodes[p]; ok {
			return i
		}
		i := len(parent)
		parent = append(parent, i)
		nodes[p] = i
		return i
	}
	root := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for _, m := range ms {
		a, b := node(observation{m.A, m.pA}), node(observation{m.B, m.pB})
		parent[root(b)] = root(a)
	}
	groups := map[int]bool{}
	for _, m := range ms {
		id := root(nodes[observation{m.A, m.pA}])
		held, seen := groups[id]
		if !seen {
			held = len(groups)%5 == 0
			groups[id] = held
		}
		if held {
			validation = append(validation, m)
		} else {
			train = append(train, m)
		}
	}
	return
}
