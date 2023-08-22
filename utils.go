package main

import "bytes"

func repeatByte(c byte, count int) string {
	var b bytes.Buffer
	for i := 0; i < count; i++ {
		b.WriteByte(c)
	}
	return b.String()
}
