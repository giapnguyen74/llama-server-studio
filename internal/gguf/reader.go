package gguf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// GGUFValueType represents the GGUF metadata value types.
type GGUFValueType uint32

const (
	TypeUINT8   GGUFValueType = 0
	TypeINT8    GGUFValueType = 1
	TypeUINT16  GGUFValueType = 2
	TypeINT16   GGUFValueType = 3
	TypeUINT32  GGUFValueType = 4
	TypeINT32   GGUFValueType = 5
	TypeFLOAT32 GGUFValueType = 6
	TypeBOOL    GGUFValueType = 7
	TypeSTRING  GGUFValueType = 8
	TypeARRAY   GGUFValueType = 9
	TypeUINT64  GGUFValueType = 10
	TypeINT64   GGUFValueType = 11
	TypeFLOAT64 GGUFValueType = 12
)

// ReadMetadata opens a GGUF file and extracts its metadata key-value header.
// It avoids loading tensor data by stopping after parsing all metadata keys.
func ReadMetadata(path string) (map[string]interface{}, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Magic: 4 bytes 'GGUF'
	var magic [4]byte
	if _, err := io.ReadFull(file, magic[:]); err != nil {
		return nil, fmt.Errorf("failed to read magic: %w", err)
	}
	if string(magic[:]) != "GGUF" {
		return nil, errors.New("invalid GGUF magic header")
	}

	// Version: uint32
	var version uint32
	if err := binary.Read(file, binary.LittleEndian, &version); err != nil {
		return nil, fmt.Errorf("failed to read version: %w", err)
	}
	if version != 1 && version != 2 && version != 3 {
		return nil, fmt.Errorf("unsupported GGUF version: %d", version)
	}

	// Tensor count and KV count depend on GGUF version
	var tensorCount uint64
	var kvCount uint64

	if version == 1 {
		var tc, kvc uint32
		if err := binary.Read(file, binary.LittleEndian, &tc); err != nil {
			return nil, err
		}
		if err := binary.Read(file, binary.LittleEndian, &kvc); err != nil {
			return nil, err
		}
		tensorCount = uint64(tc)
		kvCount = uint64(kvc)
	} else {
		if err := binary.Read(file, binary.LittleEndian, &tensorCount); err != nil {
			return nil, err
		}
		if err := binary.Read(file, binary.LittleEndian, &kvCount); err != nil {
			return nil, err
		}
	}

	metadata := make(map[string]interface{})
	metadata["gguf.version"] = version
	metadata["gguf.tensor_count"] = tensorCount

	// Read each metadata key-value pair
	for i := uint64(0); i < kvCount; i++ {
		key, err := readString(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read key at index %d: %w", i, err)
		}

		var valType uint32
		if err := binary.Read(file, binary.LittleEndian, &valType); err != nil {
			return nil, fmt.Errorf("failed to read value type for key '%s': %w", key, err)
		}

		val, err := readValue(file, GGUFValueType(valType))
		if err != nil {
			return nil, fmt.Errorf("failed to read value for key '%s': %w", key, err)
		}

		metadata[key] = val
	}

	return metadata, nil
}

func readString(r io.Reader) (string, error) {
	var length uint64
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return "", err
	}
	// Limit string length to 1MB to prevent out of memory on corrupted files
	if length > 1024*1024 {
		return "", fmt.Errorf("string length exceeds sanity limit: %d bytes", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func readValue(r io.Reader, valType GGUFValueType) (interface{}, error) {
	switch valType {
	case TypeUINT8:
		var v uint8
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case TypeINT8:
		var v int8
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case TypeUINT16:
		var v uint16
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case TypeINT16:
		var v int16
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case TypeUINT32:
		var v uint32
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case TypeINT32:
		var v int32
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case TypeFLOAT32:
		var v float32
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case TypeBOOL:
		var v bool
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case TypeSTRING:
		return readString(r)
	case TypeUINT64:
		var v uint64
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case TypeINT64:
		var v int64
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case TypeFLOAT64:
		var v float64
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case TypeARRAY:
		var arrayType uint32
		var arrayLen uint64

		if err := binary.Read(r, binary.LittleEndian, &arrayType); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &arrayLen); err != nil {
			return nil, err
		}

		// Cap size of array in memory to avoid huge vocab memory usage (e.g. tokenizer tokens)
		// We can still skip the bytes to advance the reader correctly.
		maxParsedElements := uint64(50)
		var result []interface{}
		
		for idx := uint64(0); idx < arrayLen; idx++ {
			elem, err := readValue(r, GGUFValueType(arrayType))
			if err != nil {
				return nil, err
			}
			if idx < maxParsedElements {
				result = append(result, elem)
			}
		}
		return result, nil

	default:
		return nil, fmt.Errorf("unknown GGUF value type: %d", valType)
	}
}
