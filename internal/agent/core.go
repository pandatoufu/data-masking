package agent

import (
	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
)

// Masker 抽象共享业务核心，便于测试注入受控结果；生产实现直接调用 core。
type Masker interface {
	Mask(fileName string, data []byte, opts core.MaskOptions, cfg *config.Config, auditLog *audit.Log) (*core.MaskOutput, error)
	Restore(fileName string, data, mappingData []byte, cfg *config.Config, auditLog *audit.Log) (*core.RestoreOutput, error)
}

// DefaultMasker 是 Masker 的生产实现。
type DefaultMasker struct{}

func (DefaultMasker) Mask(fileName string, data []byte, opts core.MaskOptions, cfg *config.Config, auditLog *audit.Log) (*core.MaskOutput, error) {
	return core.MaskFile(fileName, data, opts, cfg, auditLog)
}

func (DefaultMasker) Restore(fileName string, data, mappingData []byte, cfg *config.Config, auditLog *audit.Log) (*core.RestoreOutput, error) {
	return core.RestoreFile(fileName, data, mappingData, cfg, auditLog)
}
