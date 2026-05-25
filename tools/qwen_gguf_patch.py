#!/usr/bin/env python3
import sys
import os
import struct

# GGUF constants
GGUF_MAGIC = b"GGUF"

def usage():
    print("In-Place GGUF Metadata Patcher Tool for Qwen 3.5 / 3.6")
    print("Usage: ./tools/qwen_gguf_patch.py <model.gguf> [--revert]")
    print("Description: Patches 'rope.dimension_sections' from [11, 11, 10] to [11, 11, 10, 0] IN-PLACE.")
    print("             Pass --revert to transform back from [11, 11, 10, 0] to [11, 11, 10] IN-PLACE.")
    print("             No file copies are made, saving time and disk space on multi-gigabyte models.")
    sys.exit(1)

def read_string(f):
    length = struct.unpack("<Q", f.read(8))[0]
    return f.read(length)

def write_string(b_str):
    return struct.pack("<Q", len(b_str)) + b_str

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

    if len(args_list) < 1:
        usage()

    model_path = args_list[0]

    if not os.path.exists(model_path):
        print(f"Error: Model file '{model_path}' does not exist.")
        sys.exit(1)

    action_name = "Reverting" if revert else "Patching"
    print(f"Opening '{model_path}' for in-place {action_name}...")

    # Open the file in read-write binary mode
    with open(model_path, "r+b") as f:
        # 1. Read GGUF Header
        magic = f.read(4)
        if magic != GGUF_MAGIC:
            print("Error: Not a valid GGUF file.")
            sys.exit(1)

        version = struct.unpack("<I", f.read(4))[0]
        tensor_count = struct.unpack("<Q", f.read(8))[0]
        metadata_kv_count = struct.unpack("<Q", f.read(8))[0]

        print(f"GGUF Version: {version}")
        print(f"Metadata Key-Value count: {metadata_kv_count}")
        print(f"Tensor count: {tensor_count}")

        target_kv_offset = None
        target_key = None
        target_sub_type = None
        target_elements = None
        
        alignment = 32 # Default GGUF alignment

        # 2. Iterate through KV pairs and record offsets
        for i in range(metadata_kv_count):
            kv_start = f.tell()
            key = read_string(f)
            val_type = struct.unpack("<I", f.read(4))[0]
            
            # Record alignment if found
            if key == b"general.alignment" and val_type in (4, 5):
                alignment = struct.unpack("<I", f.read(4))[0]
                f.seek(-4, 1) # seek back so standard skip works
            
            # Check for the rope dimension key
            if key in (
                b"qwen36.rope.dimension_sections", b"qwen3_6.rope.dimension_sections",
                b"qwen35.rope.dimension_sections", b"qwen3_5.rope.dimension_sections",
                b"qwen3.rope.dimension_sections", b"qwen2.rope.dimension_sections"
            ):
                target_kv_offset = kv_start
                target_key = key
                
                # Check value structure
                if val_type == 9: # Array
                    sub_type = struct.unpack("<I", f.read(4))[0]
                    arr_len = struct.unpack("<Q", f.read(8))[0]
                    target_sub_type = sub_type
                    
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
                    
                    target_elements = elements
                    f.seek(-12 - len(elements)*struct.calcsize("B" if sub_type in (0,1) else "H" if sub_type in (2,3) else "I" if sub_type in (4,5) else "Q"), 1) # seek back
                else:
                    _ = skip_value(f, val_type)
            else:
                _ = skip_value(f, val_type)

        if target_kv_offset is None or target_elements is None:
            print("Finished: No matching target Qwen RoPE metadata keys found in this GGUF file.")
            sys.exit(0)

        print(f"Found target key: '{target_key.decode('utf-8')}' at file offset {target_kv_offset}")
        print(f"Current array contents: {target_elements}")

        # Check if patch is needed
        if revert:
            if len(target_elements) != 4 or target_elements[3] != 0:
                print("Finished: Model does not have a patched [11, 11, 10, 0] structure. Revert not needed.")
                sys.exit(0)
        else:
            if len(target_elements) == 4:
                print("Finished: Model is already patched with 4 elements. Nothing to do.")
                sys.exit(0)
            if len(target_elements) != 3:
                print(f"Error: Expected array length 3, found {len(target_elements)}. Cannot patch.")
                sys.exit(1)

        # 3. Read the rest of the metadata section + Tensor Infos into memory
        # Save our current offset which is the end of the KV pairs block (start of Tensor Infos)
        kv_end_offset = f.tell()
        
        # Read the entire Tensor Info block
        tensor_infos_bytes = b""
        for t in range(tensor_count):
            name = read_string(f)
            n_dims = struct.unpack("<I", f.read(4))[0]
            dims = f.read(8 * n_dims)
            t_type = f.read(4)
            offset = f.read(8)
            
            tensor_infos_bytes += write_string(name)
            tensor_infos_bytes += struct.pack("<I", n_dims)
            tensor_infos_bytes += dims
            tensor_infos_bytes += t_type
            tensor_infos_bytes += offset

        tensor_infos_end_offset = f.tell()
        
        # 4. Calculate exact original tensor data alignment point
        tensor_data_start_offset = ((tensor_infos_end_offset + alignment - 1) // alignment) * alignment
        print(f"Alignment: {alignment}")
        print(f"Original Tensor Infos end offset: {tensor_infos_end_offset}")
        print(f"Original Tensor Data start offset: {tensor_data_start_offset}")

        # 5. Build modified target Key-Value pair
        new_elements = list(target_elements)
        if revert:
            new_elements.pop()
        else:
            new_elements.append(0)
            
        print(f"New array contents: {new_elements}")

        # Build modified target KV bytes
        target_kv_bytes = write_string(target_key)
        target_kv_bytes += struct.pack("<I", 9) # val_type ARRAY = 9
        target_kv_bytes += struct.pack("<I", target_sub_type) # subtype
        target_kv_bytes += struct.pack("<Q", len(new_elements)) # length
        for el in new_elements:
            if target_sub_type in (0, 1):
                target_kv_bytes += struct.pack("<B", el)
            elif target_sub_type in (2, 3):
                target_kv_bytes += struct.pack("<H", el)
            elif target_sub_type in (4, 5):
                target_kv_bytes += struct.pack("<I", el)
            elif target_sub_type in (10, 11):
                target_kv_bytes += struct.pack("<Q", el)

        # 6. Read subsequent Key-Values from target KV end to KV end
        f.seek(target_kv_offset)
        # Skip the original target Key-Value pair
        _ = read_string(f)
        orig_val_type = struct.unpack("<I", f.read(4))[0]
        _ = skip_value(f, orig_val_type)
        
        # Read the rest of KV bytes
        post_target_kv_bytes = f.read(kv_end_offset - f.tell())

        # 7. Write the new metadata section back in-place
        f.seek(target_kv_offset)
        f.write(target_kv_bytes)
        f.write(post_target_kv_bytes)
        f.write(tensor_infos_bytes)
        
        new_tensor_infos_end_offset = f.tell()
        print(f"New Tensor Infos end offset: {new_tensor_infos_end_offset}")

        # 8. Check padding safety and pad to standard start
        if new_tensor_infos_end_offset > tensor_data_start_offset:
            print("CRITICAL ERROR: New metadata does not fit within the GGUF file's original alignment padding.")
            print("Cannot patch in-place. A full copy of the model weights is required.")
            sys.exit(1)

        # Write alignment padding zeros up to standard start
        padding_needed = tensor_data_start_offset - new_tensor_infos_end_offset
        print(f"Writing {padding_needed} bytes of alignment padding...")
        f.write(b"\x00" * padding_needed)

        # Truncate at current offset in case it decreased (revert)
        f.truncate()

        status_word = "reverted" if revert else "patched"
        print(f"SUCCESS: GGUF model '{model_path}' successfully {status_word} IN-PLACE in milliseconds!")

if __name__ == "__main__":
    main()
