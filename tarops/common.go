// © Copyright Deduptar Authors (see CONTRIBUTORS.md)
package tarops

import (
	"fmt"
	"io"
	"os"
)

type nodeID struct {
	dev   uint64
	inode uint64
}

func humanize_tar_recordtype(typeflag byte) (recordtype string) {
	recordtype = tar_typemap[typeflag]
	if recordtype == "" {
		recordtype = string(typeflag)
	}
	return recordtype
}

func tell(infile *os.File) (offset int64) {
	offset, err := infile.Seek(0, io.SeekCurrent)
	if err != nil {
		// EINVAL or ESPIPE, unimaginable, yet here we are.
		panic(fmt.Sprintf("Unexpected error while seeking: %v\n", err))
	}
	return offset
}

func send_message(messagechan *(chan ProgressMessage), messagetype int, message string) {
	if *messagechan != nil {
		*messagechan <- ProgressMessage{Type: messagetype, Message: message}
	}
}

func warning_message(messagechan *(chan ProgressMessage), message string) {
	send_message(messagechan, WarningMessage, message)
}

func verbose_message(messagechan *(chan ProgressMessage), message string) {
	send_message(messagechan, VerboseMessage, message)
}
