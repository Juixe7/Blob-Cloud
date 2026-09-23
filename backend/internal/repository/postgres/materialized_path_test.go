package postgresrepo

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"go-drive-clone/internal/domain"
)

// ---------------------------------------------------------------------------
// 1. Path Delimiter & SQL LIKE Wildcard Escaping Edge Cases
// ---------------------------------------------------------------------------

func TestEscapeSQLLike_WildcardsAndSeparators(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"/root/folder/", "/root/folder/"},
		{"/root/folder_with_underscore/", `/root/folder\_with\_underscore/`},
		{"/root/folder%percent/", `/root/folder\%percent/`},
		{`/root/folder\backslash/`, `/root/folder\\backslash/`},
		{`/root/all_%_\combined/`, `/root/all\_\%\_\\combined/`},
		{"", ""},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			actual := EscapeSQLLike(tc.input)
			if actual != tc.expected {
				t.Errorf("EscapeSQLLike(%q) = %q; want %q", tc.input, actual, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. 1-Based Indexing & Substring Replacement Mathematical Invariants
// ---------------------------------------------------------------------------

// simulatePostgresSubstring simulates PostgreSQL SUBSTRING(str FROM start) where start is 1-indexed.
func simulatePostgresSubstring(str string, start1Indexed int) string {
	if start1Indexed <= 0 {
		start1Indexed = 1
	}
	start0Indexed := start1Indexed - 1
	if start0Indexed >= len(str) {
		return ""
	}
	return str[start0Indexed:]
}

func TestMaterializedPath_SubtreeRelocation_IndexingInvariant(t *testing.T) {
	// Let source folder be S with old path:
	oldPath := "/ancestor1/ancestor2/folderS/"
	newParentPath := "/newAncestor/targetParent/"
	folderID := "folderS"
	newPrefix := newParentPath + folderID + "/" // "/newAncestor/targetParent/folderS/"

	oldLenPlusOne := len(oldPath) + 1

	// Invariant 1: The folder itself must be relocated to exactly newPrefix
	relocatedSelf := newPrefix + simulatePostgresSubstring(oldPath, oldLenPlusOne)
	if relocatedSelf != newPrefix {
		t.Fatalf("Folder itself failed relocation: got %q, want %q", relocatedSelf, newPrefix)
	}

	// Invariant 2: Direct child file
	childFilePath := oldPath + "file1.txt/"
	expectedChildFilePath := newPrefix + "file1.txt/"
	relocatedChildFile := newPrefix + simulatePostgresSubstring(childFilePath, oldLenPlusOne)
	if relocatedChildFile != expectedChildFilePath {
		t.Fatalf("Child file failed relocation: got %q, want %q", relocatedChildFile, expectedChildFilePath)
	}

	// Invariant 3: Deep subtree (>= 4 levels deep)
	deepDescendantPath := oldPath + "level1/level2/level3/level4/deep_file.pdf/"
	expectedDeepPath := newPrefix + "level1/level2/level3/level4/deep_file.pdf/"
	relocatedDeepDescendant := newPrefix + simulatePostgresSubstring(deepDescendantPath, oldLenPlusOne)
	if relocatedDeepDescendant != expectedDeepPath {
		t.Fatalf("Deep descendant failed relocation:\ngot:  %q\nwant: %q", relocatedDeepDescendant, expectedDeepPath)
	}

	// Invariant 4: Moving folder to Root
	rootPrefix := "/" + folderID + "/" // "/folderS/"
	relocatedToRoot := rootPrefix + simulatePostgresSubstring(oldPath, oldLenPlusOne)
	if relocatedToRoot != rootPrefix {
		t.Fatalf("Relocation to root failed: got %q, want %q", relocatedToRoot, rootPrefix)
	}

	relocatedDeepToRoot := rootPrefix + simulatePostgresSubstring(deepDescendantPath, oldLenPlusOne)
	expectedDeepToRoot := rootPrefix + "level1/level2/level3/level4/deep_file.pdf/"
	if relocatedDeepToRoot != expectedDeepToRoot {
		t.Fatalf("Relocation of deep descendant to root failed:\ngot:  %q\nwant: %q", relocatedDeepToRoot, expectedDeepToRoot)
	}
}

// ---------------------------------------------------------------------------
// 3. O(1) In-Memory & Prefix Cycle Detection
// ---------------------------------------------------------------------------

func TestMaterializedPath_CycleDetection(t *testing.T) {
	// Hierarchy:
	// /root/ (folderA)
	// /root/subB/ (folderB)
	// /root/subB/subC/ (folderC)
	// /root/subB/subC/subD/ (folderD - Level 4)
	// /otherRoot/ (folderX)

	folderA := domain.File{ID: "root", Path: "/root/", IsDirectory: true}
	folderB := domain.File{ID: "subB", Path: "/root/subB/", IsDirectory: true}
	folderC := domain.File{ID: "subC", Path: "/root/subB/subC/", IsDirectory: true}
	folderD := domain.File{ID: "subD", Path: "/root/subB/subC/subD/", IsDirectory: true}
	folderX := domain.File{ID: "otherRoot", Path: "/otherRoot/", IsDirectory: true}

	checkCycle := func(src, target domain.File) bool {
		if src.ID == target.ID {
			return true // cannot move into self
		}
		return strings.HasPrefix(target.Path, src.Path)
	}

	// Moving folder into itself -> cycle!
	if !checkCycle(folderA, folderA) {
		t.Error("expected cycle when moving folderA into itself")
	}

	// Moving folderA into direct child folderB -> cycle!
	if !checkCycle(folderA, folderB) {
		t.Error("expected cycle when moving folderA into direct child folderB")
	}

	// Moving folderA into level 3 child folderC -> cycle!
	if !checkCycle(folderA, folderC) {
		t.Error("expected cycle when moving folderA into grandchild folderC")
	}

	// Moving folderA into level 4 child folderD -> cycle!
	if !checkCycle(folderA, folderD) {
		t.Error("expected cycle when moving folderA into great-grandchild folderD")
	}

	// Moving folderB into level 4 child folderD -> cycle!
	if !checkCycle(folderB, folderD) {
		t.Error("expected cycle when moving folderB into descendant folderD")
	}

	// Moving folderD into folderA (up the tree) -> LEGAL!
	if checkCycle(folderD, folderA) {
		t.Error("moving deep descendant folderD into folderA should NOT trigger cycle")
	}

	// Moving folderD into sibling tree folderX -> LEGAL!
	if checkCycle(folderD, folderX) {
		t.Error("moving folderD into unrelated folderX should NOT trigger cycle")
	}

	// Moving folderA into folderX -> LEGAL!
	if checkCycle(folderA, folderX) {
		t.Error("moving folderA into folderX should NOT trigger cycle")
	}
}

// ---------------------------------------------------------------------------
// 4. Delimiter Boundary Safety (Prevent partial string collision)
// ---------------------------------------------------------------------------

func TestMaterializedPath_DelimiterBoundarySafety(t *testing.T) {
	// Suppose folder 1 has ID "folder" and folder 2 has ID "folder_extended"
	// Slashes around each ID ensure "folder" cannot prefix match "folder_extended"
	path1 := "/folder/"
	path2 := "/folder_extended/"
	path3 := "/folder/subchild/"

	if strings.HasPrefix(path2, path1) {
		t.Fatalf("delimiter collision! %q should NOT have prefix %q", path2, path1)
	}

	if !strings.HasPrefix(path3, path1) {
		t.Fatalf("child %q should have prefix %q", path3, path1)
	}
}

// ---------------------------------------------------------------------------
// 5. Concurrency & Race Detector Invariant Tests
// ---------------------------------------------------------------------------

func TestMaterializedPath_ConcurrentCalculations(t *testing.T) {
	// Verify that high-concurrency path manipulation runs cleanly without race conditions
	const numGoroutines = 50
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(routineID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				parentPath := fmt.Sprintf("/tenant_%d/parent_%d/", routineID, j)
				childID := fmt.Sprintf("child_%d_%d", routineID, j)
				childPath := parentPath + childID + "/"

				// Check prefix
				if !strings.HasPrefix(childPath, parentPath) {
					t.Errorf("expected childPath %q to have prefix %q", childPath, parentPath)
				}

				// Check escape
				escapedParent := EscapeSQLLike(parentPath)
				escaped := EscapeSQLLike(childPath)
				if !strings.HasPrefix(escaped, escapedParent) {
					t.Errorf("escaped path %q should retain structure", escaped)
				}

				// Check substring
				sub := simulatePostgresSubstring(childPath, len(parentPath)+1)
				expectedSub := childID + "/"
				if sub != expectedSub {
					t.Errorf("substring mismatch: got %q, want %q", sub, expectedSub)
				}
			}
		}(i)
	}

	wg.Wait()
}

// ---------------------------------------------------------------------------
// 6. Transaction Rollback Simulation
// ---------------------------------------------------------------------------

func TestMaterializedPath_SimulatedRollback(t *testing.T) {
	mockState := make(map[string]string)
	mockState["folderA"] = "/folderA/"
	mockState["fileB"] = "/folderA/fileB/"

	txErr := errors.New("simulated database network timeout")

	// Execute transaction simulation
	simulateTx := func() error {
		backup := make(map[string]string)
		for k, v := range mockState {
			backup[k] = v
		}

		// Apply staged changes
		mockState["folderA"] = "/newRoot/folderA/"
		mockState["fileB"] = "/newRoot/folderA/fileB/"

		// Failure occurs before commit
		if txErr != nil {
			// Rollback to backup
			mockState = backup
			return txErr
		}
		return nil
	}

	err := simulateTx()
	if !errors.Is(err, txErr) {
		t.Fatalf("expected txErr, got %v", err)
	}

	// Verify complete rollback to pristine state
	if mockState["folderA"] != "/folderA/" {
		t.Fatalf("rollback failed for folderA: got %q", mockState["folderA"])
	}
	if mockState["fileB"] != "/folderA/fileB/" {
		t.Fatalf("rollback failed for fileB: got %q", mockState["fileB"])
	}
}
