package queue

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go-drive-clone/internal/domain"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeFileRepo satisfies fileGetter. It returns a configured file for a known
// id, or an error otherwise.
type fakeFileRepo struct {
	files map[string]*domain.File
	err   error // when set, GetByID always fails
}

func (f *fakeFileRepo) GetByID(_ context.Context, id string) (*domain.File, error) {
	if f.err != nil {
		return nil, f.err
	}
	if file, ok := f.files[id]; ok {
		return file, nil
	}
	return nil, errors.New("file not found")
}

// fakeBlockRepo satisfies blockHashLister. It returns a fixed ordered list of
// hashes for a file id.
type fakeBlockRepo struct {
	hashes map[string][]string
	err    error
}

func (b *fakeBlockRepo) ListFileBlockHashes(_ context.Context, fileID string) ([]string, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.hashes[fileID], nil
}

// memStorage is an in-memory StorageProvider. PutObject stores bytes keyed by
// the object key; GetObject reads them back. This lets a test verify the exact
// thumbnail key the processor produced.
type memStorage struct {
	objects map[string][]byte
	putErr  error
	getErr  error
}

func newMemStorage() *memStorage {
	return &memStorage{objects: map[string][]byte{}}
}

func (m *memStorage) GenerateUploadURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (m *memStorage) PutObject(_ context.Context, key string, reader io.Reader, _ int64, _ string) error {
	if m.putErr != nil {
		return m.putErr
	}
	b, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	m.objects[key] = b
	return nil
}
func (m *memStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	b, ok := m.objects[key]
	if !ok {
		return nil, errors.New("object not found: " + key)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *memStorage) DeleteObject(_ context.Context, key string) error {
	delete(m.objects, key)
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// encodePNG encodes a solid-colour RGBA image of the given dimensions. Used to
// build a valid source image that the processor can decode and resize.
func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// isSupportedImage
// ---------------------------------------------------------------------------

func TestIsSupportedImage(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"lowercase jpg", "photo.jpg", true},
		{"lowercase jpeg", "photo.jpeg", true},
		{"lowercase png", "photo.png", true},
		{"lowercase webp", "photo.webp", true},
		{"uppercase PNG", "PHOTO.PNG", true},
		{"mixed case JpEg", "Photo.JpEg", true},
		{"extension in path", "dir/sub/photo.png", true},
		{"txt not supported", "notes.txt", false},
		{"gif not supported", "anim.gif", false},
		{"no extension", "README", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSupportedImage(tc.in); got != tc.want {
				t.Fatalf("isSupportedImage(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// generateThumbnail
// ---------------------------------------------------------------------------

func TestGenerateThumbnail_LargeImageIsResized(t *testing.T) {
	src := encodePNG(t, 800, 600)
	thumb, err := generateThumbnail(src)
	if err != nil {
		t.Fatalf("generateThumbnail: %v", err)
	}

	img, err := png.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() > ThumbnailMaxSize || bounds.Dy() > ThumbnailMaxSize {
		t.Fatalf("thumbnail %dx%d exceeds max %d", bounds.Dx(), bounds.Dy(), ThumbnailMaxSize)
	}
	// 800x600 scaled to fit 200 box -> longest side 200 -> 200x150.
	if bounds.Dx() != 200 || bounds.Dy() != 150 {
		t.Fatalf("thumbnail dimensions = %dx%d, want 200x150", bounds.Dx(), bounds.Dy())
	}
}

func TestGenerateThumbnail_SmallImageUnchanged(t *testing.T) {
	src := encodePNG(t, 50, 40)
	thumb, err := generateThumbnail(src)
	if err != nil {
		t.Fatalf("generateThumbnail: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 50 || bounds.Dy() != 40 {
		t.Fatalf("small image was resized: %dx%d, want 50x40", bounds.Dx(), bounds.Dy())
	}
}

func TestGenerateThumbnail_InvalidBytes(t *testing.T) {
	if _, err := generateThumbnail([]byte("not an image")); err == nil {
		t.Fatal("expected error decoding invalid bytes, got nil")
	}
}

// ---------------------------------------------------------------------------
// ThumbnailProcessor.ProcessMessage
// ---------------------------------------------------------------------------

func TestProcessMessage_NonImageFileSkipped(t *testing.T) {
	// A non-image file should be skipped (nil error so the worker deletes it),
	// and NO thumbnail should be uploaded.
	storage := newMemStorage()
	proc := &ThumbnailProcessor{
		files:   &fakeFileRepo{files: map[string]*domain.File{"f1": {ID: "f1", Name: "report.pdf"}}},
		blocks:  &fakeBlockRepo{},
		storage: storage,
		log:     testLogger(),
	}

	err := proc.ProcessMessage(context.Background(), ThumbnailMessage{FileID: "f1"})
	if err != nil {
		t.Fatalf("non-image should return nil (delete), got %v", err)
	}
	for key := range storage.objects {
		if strings.HasPrefix(key, "thumbnails/") {
			t.Fatalf("unexpected thumbnail uploaded for non-image: %s", key)
		}
	}
}

func TestProcessMessage_GeneratesAndUploadsThumbnail(t *testing.T) {
	src := encodePNG(t, 400, 400)
	storage := newMemStorage()
	storage.objects["blocks/hash-abc"] = src

	proc := &ThumbnailProcessor{
		files:   &fakeFileRepo{files: map[string]*domain.File{"f1": {ID: "f1", Name: "cat.png"}}},
		blocks:  &fakeBlockRepo{hashes: map[string][]string{"f1": {"hash-abc"}}},
		storage: storage,
		log:     testLogger(),
	}

	err := proc.ProcessMessage(context.Background(), ThumbnailMessage{FileID: "f1", UserID: "u1"})
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}

	// Thumbnail must be stored at the documented key and be a decodable PNG.
	thumb, ok := storage.objects["thumbnails/f1.png"]
	if !ok {
		t.Fatal("thumbnail not uploaded to thumbnails/f1.png")
	}
	if _, err := png.Decode(bytes.NewReader(thumb)); err != nil {
		t.Fatalf("uploaded thumbnail is not a valid PNG: %v", err)
	}
}

func TestProcessMessage_MultipleBlocksAssembledInOrder(t *testing.T) {
	// Build a source PNG, split it into 3 chunks, and store each under its own
	// hash. The processor must fetch them in sequence order and concatenate to
	// recover the original image.
	full := encodePNG(t, 300, 200)
	// Split into three unequal parts.
	third := len(full) / 3
	parts := [][]byte{full[:third], full[third : 2*third], full[2*third:]}

	storage := newMemStorage()
	storage.objects["blocks/p0"] = parts[0]
	storage.objects["blocks/p1"] = parts[1]
	storage.objects["blocks/p2"] = parts[2]

	proc := &ThumbnailProcessor{
		files:  &fakeFileRepo{files: map[string]*domain.File{"f1": {ID: "f1", Name: "multi.png"}}},
		blocks: &fakeBlockRepo{hashes: map[string][]string{"f1": {"p0", "p1", "p2"}}},
		storage: storage,
		log:    testLogger(),
	}

	if err := proc.ProcessMessage(context.Background(), ThumbnailMessage{FileID: "f1"}); err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if _, ok := storage.objects["thumbnails/f1.png"]; !ok {
		t.Fatal("thumbnail not uploaded")
	}
}

func TestProcessMessage_FileNotFoundReturnsError(t *testing.T) {
	proc := &ThumbnailProcessor{
		files:   &fakeFileRepo{files: map[string]*domain.File{}},
		blocks:  &fakeBlockRepo{},
		storage: newMemStorage(),
		log:     testLogger(),
	}
	err := proc.ProcessMessage(context.Background(), ThumbnailMessage{FileID: "missing"})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestProcessMessage_BlockFetchError(t *testing.T) {
	storage := newMemStorage()
	storage.getErr = errors.New("storage down")
	proc := &ThumbnailProcessor{
		files:   &fakeFileRepo{files: map[string]*domain.File{"f1": {ID: "f1", Name: "pic.png"}}},
		blocks:  &fakeBlockRepo{hashes: map[string][]string{"f1": {"nope"}}},
		storage: storage,
		log:     testLogger(),
	}
	err := proc.ProcessMessage(context.Background(), ThumbnailMessage{FileID: "f1"})
	if err == nil {
		t.Fatal("expected error when block fetch fails, got nil")
	}
}
