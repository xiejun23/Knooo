package studio

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func protectModelKey(_ string, key []byte, binding []byte, decrypt bool) ([]byte, error) {
	input := windows.DataBlob{Size: uint32(len(key)), Data: &key[0]}
	entropy := windows.DataBlob{Size: uint32(len(binding)), Data: &binding[0]}
	var output windows.DataBlob
	var err error
	if decrypt {
		err = windows.CryptUnprotectData(&input, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	} else {
		err = windows.CryptProtectData(&input, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}
