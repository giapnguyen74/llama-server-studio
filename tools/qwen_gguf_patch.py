#!/usr/bin/env python3
import sys
import os
import struct

# GGUF constants
GGUF_MAGIC = b"GGUF"
GGUF_VERSION = 3 

def usage():
    print("GGUF Metadata Patcher Tool for Qwen 3.5 / 3.6")
    print("Usage: ./tools/qwen_gguf_patch.py <input.gguf> <output.gguf> [--revert]")
    print("Description: Patches 'rope.dimension_sections' from [11, 11, 10] to [11, 11, 10, 0]")
    print("             Pass --revert to transform back from [11, 11, 10, 0] to [11, 11, 10]")
    sys.exit(1)

def read_string(f):
    length = struct.unpack("<Q", f.read(8))[0]
    return f.read(length)

def write_string(out, b_str):
    out.write(struct.pack("<Q", len(b_str)))
    out.write(b_str)

def skip_value(f, val_type):
    # UINT8 = 0, INT8 = 1, UINT16 = 2, INT16 = 3, UINT32 = 4, INT32 = 5, FLOAT32 = 6, BOOL = 7, STRING = 8, ARRAY = 9, UINT64 = 10, INT64 = 11, FLOAT64 = 12
    if val_type in (0, 1, 7): # 1 byte
        return f.read(1)
    elif val_type in (2, 3): # 2 bytes
        return f.read(2)
    elif val_type in (4, 5, 6): # 4 bytes
        return f.read(4)
    elif val_type in (10, 11, 12): # 8 bytes
        return f.read(8)
    elif val_type == 8: # String
        length = struct.unpack("<Q", f.read(8))[0]
        return struct.pack("<Q", length) + f.read(length)
    elif val_type == 9: # Array
        sub_type = struct.unpack("<I", f.read(4))[0]
        length = struct.unpack("<Q", f.read(8))[0]
        arr_data = b""
        for _ in range(length):
            arr_data += skip_value(f, sub_type)
        return struct.pack("<I", sub_type) + struct.pack("<Q", length) + arr_data
    else:
        raise ValueError(f"Unknown value type: {val_type}")

def main():
    revert = False
    args_list = sys.argv[1:]
    if "--revert" in args_list:
        revert = True
        args_list.remove("--revert")

    if len(args_list) < 2:
        usage()

    in_path = args_list[0]
    out_path = args_list[1]

    if not os.path.exists(in_path):
        print(f"Error: Input file '{in_path}' does not exist.")
        sys.exit(1)

    action_name = "Reverting" if revert else "Patching"
    print(f"Reading '{in_path}' for {action_name}...")
    
    with open(in_path, "rb") as f, open(out_path, "wb") as out:
        # 1. Read GGUF Header
        magic = f.read(4)
        if magic != GGUF_MAGIC:
            print("Error: Not a valid GGUF file.")
            sys.exit(1)
        out.write(magic)

        version = struct.unpack("<I", f.read(4))[0]
        out.write(struct.pack("<I", version))

        tensor_count = struct.unpack("<Q", f.read(8))[0]
        out.write(struct.pack("<Q", tensor_count))

        metadata_kv_count = struct.unpack("<Q", f.read(8))[0]
        out.write(struct.pack("<Q", metadata_kv_count))

        print(f"Metadata Key-Value count: {metadata_kv_count}")
        print(f"Tensor count: {tensor_count}")

        patched = False

        # 2. Iterate through KV pairs
        for i in range(metadata_kv_count):
            key = read_string(f)
            val_type = struct.unpack("<I", f.read(4))[0]
            
            # Check for the rope dimension key
            if key in (
                b"qwen36.rope.dimension_sections", b"qwen3_6.rope.dimension_sections",
                b"qwen35.rope.dimension_sections", b"qwen3_5.rope.dimension_sections",
                b"qwen3.rope.dimension_sections", b"qwen2.rope.dimension_sections"
            ):
                print(f"Found target key: {key.decode('utf-8')}")
                
                # Check value structure
                if val_type == 9: # Array
                    sub_type = struct.unpack("<I", f.read(4))[0]
                    arr_len = struct.unpack("<Q", f.read(8))[0]
                    
                    print(f"Current array type: {sub_type}, length: {arr_len}")
                    
                    # Read current values
                    elements = []
                    for _ in range(arr_len):
                        if sub_type in (0, 1):
                            elements.append(struct.unpack("<B", f.read(1))[0])
                        elif sub_type in (2, 3):
                            elements.append(struct.unpack("<H", f.read(2))[0])
                        elif sub_type in (4, 5):
                            elements.append(struct.unpack("<I", f.read(4))[0])
                        elif sub_type in (10, 11):
                            elements.append(struct.unpack("<Q", f.read(8))[0])
                    
                    print(f"Current array content: {elements}")
                    
                    # Modify elements to include or remove the 4th element (0)
                    if revert:
                        if len(elements) == 4 and elements[3] == 0:
                            elements.pop()
                            patched = True
                            print(f"Reverted array content: {elements}")
                    else:
                        if len(elements) == 3:
                            elements.append(0)
                            patched = True
                            print(f"Patched array content: {elements}")
                    
                    # Write key & modified array to output
                    write_string(out, key)
                    out.write(struct.pack("<I", val_type)) 
                    out.write(struct.pack("<I", sub_type)) 
                    out.write(struct.pack("<Q", len(elements))) 
                    for el in elements:
                        if sub_type in (0, 1):
                            out.write(struct.pack("<B", el))
                        elif sub_type in (2, 3):
                            out.write(struct.pack("<H", el))
                        elif sub_type in (4, 5):
                            out.write(struct.pack("<I", el))
                        elif sub_type in (10, 11):
                            out.write(struct.pack("<Q", el))
                else:
                    val_data = skip_value(f, val_type)
                    write_string(out, key)
                    out.write(struct.pack("<I", val_type))
                    out.write(val_data)
            else:
                val_data = skip_value(f, val_type)
                write_string(out, key)
                out.write(struct.pack("<I", val_type))
                out.write(val_data)

        # 3. Handle Tensors info block and binary weights data
        print("Copying tensor metadata & binary data (this might take a while for large files)...")
        chunk_size = 64 * 1024 * 1024 
        while True:
            chunk = f.read(chunk_size)
            if not chunk:
                break
            out.write(chunk)

        if patched:
            status_word = "reverted" if revert else "patched"
            print(f"Successfully {status_word} GGUF rope dimension metadata!")
        else:
            print("Finished processing. No matching key or action was required.")

if __name__ == "__main__":
    main()
