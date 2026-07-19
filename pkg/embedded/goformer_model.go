package embedded

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/MichaelAyles/goformer"
)

// goformerInstance is a cached singleton embedding model instance.
var (
	goformerInstance   *goformer.Model
	goformerInstanceMu sync.Mutex
)

// GoformerModel wraps a goformer embedding model and provides a convenient
// interface for generating text embeddings.
type GoformerModel struct {
	model *goformer.Model
}

// NewGoformerModel loads a Safetensors embedding model from the given directory path.
// The loaded model is cached as a singleton so subsequent calls return the cached instance.
func NewGoformerModel(modelPath string) (*GoformerModel, error) {
	goformerInstanceMu.Lock()
	defer goformerInstanceMu.Unlock()

	if goformerInstance != nil {
		return &GoformerModel{model: goformerInstance}, nil
	}

	model, err := goformer.Load(modelPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load embedding model %s: %w", modelPath, err)
	}

	goformerInstance = model
	slog.Info("Loaded embedding model", "path", modelPath, "dims", model.Dims(), "max_seq_len", model.MaxSeqLen())
	return &GoformerModel{model: model}, nil
}

// Embed generates a normalised embedding vector for the input text.
func (m *GoformerModel) Embed(text string) ([]float32, error) {
	return m.model.Embed(text)
}

// EmbedBatch generates embeddings for a batch of texts.
func (m *GoformerModel) EmbedBatch(texts []string) ([][]float32, error) {
	return m.model.EmbedBatch(texts)
}

// Dims returns the embedding dimensionality.
func (m *GoformerModel) Dims() int {
	return m.model.Dims()
}

// MaxSeqLen returns the maximum sequence length the model supports.
func (m *GoformerModel) MaxSeqLen() int {
	return m.model.MaxSeqLen()
}

// ResetGoformerInstance clears the cached singleton. Used for testing.
func ResetGoformerInstance() {
	goformerInstanceMu.Lock()
	defer goformerInstanceMu.Unlock()
	goformerInstance = nil
}