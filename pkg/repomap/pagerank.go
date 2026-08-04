package repomap

// rankFiles runs a pure-Go PageRank over the file reference graph and returns
// one score per file. Files named in activeFiles get a 50x multiplier applied
// after the power iteration (see Render for the 10x symbol multiplier).
func rankFiles(index *Index, activeFiles map[string]bool) []float64 {
	n := len(index.Files)
	scores := make([]float64, n)
	if n == 0 {
		return scores
	}

	// Edge i -> j: file i references at least one symbol defined in file j.
	// Weight is the number of distinct referenced symbols (capped) so a file
	// that is genuinely depended upon ranks higher than one hit once.
	edges := make([][]int, n)
	weight := make(map[[2]int]float64, n*2)
	symbolOwner := make(map[string][]int) // symbol name -> file indices defining it
	for _, symbol := range index.Symbols {
		fileIdx, ok := index.FileIdx[symbol.File]
		if !ok {
			continue
		}
		symbolOwner[symbol.Name] = append(symbolOwner[symbol.Name], fileIdx)
	}
	for source, refs := range index.Refs {
		sourceIdx, ok := index.FileIdx[source]
		if !ok {
			continue
		}
		targets := map[int]float64{}
		for _, name := range refs {
			for _, target := range symbolOwner[name] {
				if target == sourceIdx {
					continue // self-loops do not carry authority
				}
				targets[target]++
			}
		}
		for target, count := range targets {
			if count > 10 {
				count = 10
			}
			edges[sourceIdx] = append(edges[sourceIdx], target)
			weight[[2]int{sourceIdx, target}] = count
		}
	}

	const (
		damping    = 0.85
		iterations = 40
	)
	outDegree := make([]float64, n)
	for i := 0; i < n; i++ {
		for _, j := range edges[i] {
			outDegree[i] += weight[[2]int{i, j}]
		}
	}

	rank := make([]float64, n)
	for i := range rank {
		rank[i] = 1.0 / float64(n)
	}
	base := (1 - damping) / float64(n)
	for iteration := 0; iteration < iterations; iteration++ {
		dangling := 0.0
		for i := 0; i < n; i++ {
			if outDegree[i] == 0 {
				dangling += rank[i]
			}
		}
		next := make([]float64, n)
		for i := 0; i < n; i++ {
			if outDegree[i] == 0 {
				continue
			}
			spread := rank[i] / outDegree[i]
			for _, j := range edges[i] {
				next[j] += spread * weight[[2]int{i, j}]
			}
		}
		for j := 0; j < n; j++ {
			next[j] = base + damping*(next[j]+dangling/float64(n))
		}
		rank = next
	}

	for i := 0; i < n; i++ {
		multiplier := 1.0
		if activeFiles[index.Files[i]] {
			multiplier = 50
		}
		scores[i] = rank[i] * multiplier
	}
	return scores
}
