package platform

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// decodeManifest 保留 YAML 供手写小配置使用；大规模生成的 JSON 走线性查重，
// 避免 YAML 解码器对同一 entities 映射的键两两比较。两种格式均拒绝多文档。
func decodeManifest(f *os.File, m *manifest) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	// 显式 .json 才选择 JSON，保留 YAML 的 {key: value} 流式对象语法。
	if strings.EqualFold(filepath.Ext(f.Name()), ".json") {
		d := json.NewDecoder(f)
		if err := checkJSONValue(d, 0); err != nil {
			return err
		}
		if _, err := d.Token(); err != io.EOF {
			return errors.New("one manifest required")
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		d = json.NewDecoder(f)
		d.DisallowUnknownFields()
		return d.Decode(m)
	}
	d := yaml.NewDecoder(f)
	d.KnownFields(true)
	if err := d.Decode(m); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("one manifest required")
	}
	return nil
}

// 每一层只保留该对象已经出现的键，拒绝重复键和大小写别名。
// 不构造整份 YAML AST；对实体映射查重的平均时间随实体数线性增长。
func checkJSONValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("manifest nesting too deep")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for d.More() {
			raw, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := raw.(string)
			if !ok {
				return errors.New("invalid manifest key")
			}
			if key != strings.ToLower(key) {
				return errors.New("manifest keys must be lowercase")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate manifest key")
			}
			seen[key] = struct{}{}
			if err := checkJSONValue(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := checkJSONValue(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected manifest delimiter")
	}
	_, err = d.Token()
	return err
}
