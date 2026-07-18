package embedded

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/computerex/dlgo"
)

// dlgoInstance is a cached singleton LLM instance, shared across all callers.
var (
	dlgoInstance   *dlgo.LLM
	dlgoInstanceMu sync.Mutex
)

// DlgoModel wraps a dlgo LLM and serializes concurrent inference calls.
type DlgoModel struct {
	mu  sync.Mutex
	llm *dlgo.LLM
}

// gpuLayers holds the number of model layers to offload to the GPU.
const gpuLayers = -1 // -1 means offload all layers.

// NewDlgoModel loads a GGUF model. If gpuEnabled is true, it attempts GPU
// acceleration and falls back to CPU if GPU is not available.
func NewDlgoModel(modelPath string, gpuEnabled bool) (*DlgoModel, error) {
	dlgoInstanceMu.Lock()
	defer dlgoInstanceMu.Unlock()

	if dlgoInstance != nil {
		return &DlgoModel{llm: dlgoInstance}, nil
	}

	var llm *dlgo.LLM
	var err error

	if gpuEnabled {
		// Attempt to load with GPU acceleration.
		llm, err = dlgo.LoadLLM(modelPath, 0, gpuLayers)
		if err == nil {
			slog.Info("Loaded model with GPU acceleration")
		} else {
			slog.Warn("GPU load failed, falling back to CPU", "error", err)
			llm, err = dlgo.LoadLLM(modelPath)
			if err != nil {
				return nil, fmt.Errorf("failed to load model %s: %w", modelPath, err)
			}
			dlgoInstance = llm
			return &DlgoModel{llm: llm}, nil
		}
	} else {
		llm, err = dlgo.LoadLLM(modelPath)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load model %s: %w", modelPath, err)
	}

	dlgoInstance = llm
	return &DlgoModel{llm: llm}, nil
}

// Chat runs a chat completion with a system and user message.
func (m *DlgoModel) Chat(system, user string, opts ...dlgo.Option) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.llm.Chat(system, user, opts...)
}

// ChatMessages runs a chat completion with a list of messages.
func (m *DlgoModel) ChatMessages(messages []dlgo.Message, opts ...dlgo.Option) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.llm.ChatMessages(messages, opts...)
}

// Generate runs a simple text generation (no chat format).
func (m *DlgoModel) Generate(prompt string, opts ...dlgo.Option) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.llm.Generate(prompt, opts...)
}

// Info returns the loaded model's metadata.
func (m *DlgoModel) Info() dlgo.ModelInfo {
	return m.llm.ModelInfo()
}

// ResetInstance clears the cached singleton. Used for testing.
func ResetInstance() {
	dlgoInstanceMu.Lock()
	defer dlgoInstanceMu.Unlock()
	dlgoInstance = nil
}