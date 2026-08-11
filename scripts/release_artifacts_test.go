package scripts_test

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const releaseVersion = "0.1.0"

func TestBuildReleaseArtifacts(t *testing.T) {
	repositoryRoot := filepath.Clean("..")
	outputDir := t.TempDir()
	command := exec.Command("bash", "scripts/build-release-artifacts.sh", releaseVersion, "test-commit", outputDir)
	command.Dir = repositoryRoot
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build release artifacts: %v\n%s", err, output)
	}

	wantPublicSkill := sourcePublicSkillFiles(t, repositoryRoot)
	wantSchemas := sourceSchemaFiles(t, repositoryRoot)
	targets := []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"}
	var firstFiles []string
	for _, target := range targets {
		name := fmt.Sprintf("batchscope_%s_%s", releaseVersion, target)
		archivePath := filepath.Join(outputDir, name+".tar.gz")
		files := readArchive(t, archivePath, name, wantPublicSkill, wantSchemas)
		if firstFiles == nil {
			firstFiles = files
			continue
		}
		if !reflect.DeepEqual(files, firstFiles) {
			t.Errorf("%s public file structure differs:\ngot  %v\nwant %v", target, files, firstFiles)
		}
	}

	verifyChecksums(t, outputDir, targets)
}

func sourcePublicSkillFiles(t *testing.T, repositoryRoot string) map[string][]byte {
	t.Helper()
	root := filepath.Join(repositoryRoot, "skills", "public", "batchscope")
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.ToSlash(relative), "references/schema/") {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = contents
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func sourceSchemaFiles(t *testing.T, repositoryRoot string) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repositoryRoot, "schema", "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte, len(paths))
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(path)] = contents
	}
	if len(files) == 0 {
		t.Fatal("source JSON Schema files are missing")
	}
	return files
}

func readArchive(t *testing.T, archivePath, root string, wantPublicSkill, wantSchemas map[string][]byte) []string {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()

	contents := make(map[string][]byte)
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(header.Name, root+"/")
		if name == "skills/internal" || strings.HasPrefix(name, "skills/internal/") {
			t.Errorf("%s: Internal Skill entry is included: %s", archivePath, name)
		}
		if header.FileInfo().IsDir() {
			continue
		}
		data, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatal(err)
		}
		contents[name] = data
	}

	for _, name := range []string{"batchscope", "README.md", "LICENSE", "skills/public/batchscope/SKILL.md"} {
		if _, ok := contents[name]; !ok {
			t.Errorf("%s: required file %s is missing", archivePath, name)
		}
	}
	for name, want := range wantPublicSkill {
		archiveName := "skills/public/batchscope/" + name
		if got, ok := contents[archiveName]; !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: Public Skill file %s is missing or differs", archivePath, archiveName)
		}
	}
	for name, want := range wantSchemas {
		archiveName := "skills/public/batchscope/references/schema/" + name
		if got, ok := contents[archiveName]; !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: JSON Schema %s is missing or differs", archivePath, archiveName)
		}
	}
	archiveSchemas := make(map[string][]byte)
	for name := range contents {
		if strings.HasPrefix(name, "skills/public/batchscope/references/schema/") {
			archiveSchemas[filepath.Base(name)] = contents[name]
		}
	}
	if !reflect.DeepEqual(archiveSchemas, wantSchemas) {
		t.Errorf("%s: distributed JSON Schema file set differs from schema/*.schema.json", archivePath)
	}
	if readme := string(contents["README.md"]); strings.Contains(readme, "blob/main") || !strings.Contains(readme, "blob/v"+releaseVersion) {
		t.Errorf("%s: README links are not fixed to v%s", archivePath, releaseVersion)
	}

	files := make([]string, 0, len(contents))
	for name := range contents {
		files = append(files, name)
	}
	sort.Strings(files)
	return files
}

func verifyChecksums(t *testing.T, outputDir string, targets []string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(outputDir, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(contents))
	if len(lines) != len(targets)*2 {
		t.Fatalf("checksums.txt fields = %d, want %d", len(lines), len(targets)*2)
	}
	for index := 0; index < len(lines); index += 2 {
		archivePath := filepath.Join(outputDir, lines[index+1])
		archive, err := os.ReadFile(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := fmt.Sprintf("%x", sha256.Sum256(archive)), lines[index]; got != want {
			t.Errorf("checksum for %s = %s, want %s", archivePath, got, want)
		}
	}
}
