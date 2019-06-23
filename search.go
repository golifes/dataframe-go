package dataframe

import (
	"context"
	"golang.org/x/sync/errgroup"
	"runtime"
	"sync"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// Search is used to find particular values in a given Series.
// It will find all values that are between lower and upper bounds (inclusive).
// It will return a slice containing the rows which contain values within the bounds.
// If Search is canceled, an incomplete list of the values "found so far" is returned.
func Search(ctx context.Context, s Series, lower, upper interface{}, r ...Range) ([]int, error) {

	s.Lock()
	defer s.Unlock()

	if len(r) == 0 {
		r = append(r, Range{})
	}

	var equalCheck bool
	if cmp.Equal(lower, upper, cmpopts.IgnoreUnexported()) {
		equalCheck = true
	}

	start, end, err := r[0].Limits(s.NRows(Options{DontLock: true}))
	if err != nil {
		return nil, err
	}

	nCores := runtime.NumCPU()

	// Group search range equally amongst each core
	div := (end - start + 1) / nCores

	subRanges := []Range{}

	for i := 0; i < nCores; i++ {
		var subStart int
		var subEnd int

		if i != nCores-1 {
			subStart = i * div
			subEnd = (i+1)*div - 1
		} else {
			// last code
			subStart = i * div
			subEnd = end
		}

		subRanges = append(subRanges, Range{
			Start: &subStart,
			End:   &subEnd,
		})
	}

	// Concurrently search each subRange for values in range
	var g errgroup.Group

	var mapProtect sync.Mutex
	mapRows := map[int][]int{} // For each core store the rows we have found so far

	for i := 0; i < nCores; i++ {
		i := i
		g.Go(func() error {

			rowsFound := []int{} // Store all rows that we have found

			defer func() {
				mapProtect.Lock()
				mapRows[i] = rowsFound
				mapProtect.Unlock()
			}()

			for row := *subRanges[i].Start; row < *subRanges[i].End+1; row++ {

				// Cancel for loop if context is canceled
				if err := ctx.Err(); err != nil {
					return err
				}

				val := s.Value(row, Options{DontLock: true})

				// Check if val is in range
				if equalCheck {
					if s.IsEqualFunc(val, lower) {
						rowsFound = append(rowsFound, row)
					}
				} else {
					if !s.IsLessThanFunc(val, lower) && (s.IsLessThanFunc(val, upper) || s.IsEqualFunc(val, upper)) {
						rowsFound = append(rowsFound, row)
					}
				}

			}

			return nil
		})
	}

	err = g.Wait()

	// Convert rows found to Range slice
	var rows []int
	var count int
	for i := 0; i < nCores; i++ {
		count = count + len(mapRows[i])
	}
	rows = make([]int, 0, count)

	// Store found rows into 1 int
	for i := 0; i < nCores; i++ {
		foundRows := mapRows[i]
		rows = append(rows, foundRows...)
	}

	return rows, err
}
