// +build linux,cgo,!static_build,journald

package journald

// #cgo pkg-config: libsystemd-journal
// #include <sys/types.h>
// #include <systemd/sd-journal.h>
// #include <errno.h>
// #include <stdlib.h>
// #include <string.h>
//
//static int get_message(sd_journal *j, const char **msg, size_t *length)
//{
//	int rc;
//	*msg = NULL;
//	*length = 0;
//	rc = sd_journal_get_data(j, "MESSAGE", (const void **) msg, length);
//	if (rc == 0) {
//		if (*length > 8) {
//			(*msg) += 8;
//			*length -= 8;
//		} else {
//			*msg = NULL;
//			*length = 0;
//			rc = -ENOENT;
//		}
//	}
//	return rc;
//}
//static int get_priority(sd_journal *j, int *priority)
//{
//	const void *data;
//	size_t i, length;
//	int rc;
//	*priority = -1;
//	rc = sd_journal_get_data(j, "PRIORITY", &data, &length);
//	if (rc == 0) {
//		if ((length > 9) && (strncmp(data, "PRIORITY=", 9) == 0)) {
//			*priority = 0;
//			for (i = 9; i < length; i++) {
//				*priority = *priority * 10 + ((const char *)data)[i] - '0';
//			}
//			if (length > 9) {
//				rc = 0;
//			}
//		}
//	}
//	return rc;
//}
import "C"

import (
	"fmt"
	"io"
	"time"
	"unsafe"

	"github.com/coreos/go-systemd/journal"
	"github.com/docker/docker/pkg/jsonlog"
)

func (s *Journald) IsReadable() bool {
	return true
}

func (s *Journald) GetReader(lines int, since time.Time) (io.Reader, error) {
	reader, writer := io.Pipe()
	go func() {
		var j *C.sd_journal
		var msg *C.char
		var length C.size_t
		var stamp C.uint64_t
		var sinceUnixMicro uint64
		var priority C.int

		defer writer.Close()
		// Get a handle to the journal.
		rc := C.sd_journal_open(&j, C.int(0))
		if rc != 0 {
			return
		}
		defer C.sd_journal_close(j)
		// Remove limits on the size of data items that we'll retrieve.
		rc = C.sd_journal_set_data_threshold(j, C.size_t(0))
		if rc != 0 {
			return
		}
		// Add a match to have the library do the searching for us.
		cid := "CONTAINER_ID_FULL=" + s.Jmap["CONTAINER_ID_FULL"]
		cmatch := C.CString(cid)
		if cmatch == nil {
			return
		}
		defer C.free(unsafe.Pointer(cmatch))
		rc = C.sd_journal_add_match(j, unsafe.Pointer(cmatch), C.size_t(len(cid)))
		if rc != 0 {
			return
		}
		// If we have a cutoff time, convert it to Unix time once.
		if !since.IsZero() {
			nano := since.UnixNano()
			sinceUnixMicro = uint64(nano / 1000)
		}
		log := &jsonlog.JSONLog{}
		if lines > 0 {
			// Start at the end of the journal.
			if C.sd_journal_seek_tail(j) < 0 {
				return
			}
			if C.sd_journal_previous(j) < 0 {
				return
			}
			// Walk backward.
			buffer := make([]string, 0, lines)
			if buffer == nil {
				return
			}
			for lines > 0 {
				// Stop if the entry time is before our cutoff.
				// We'll need the entry time if it isn't, so go
				// ahead and parse it now.
				if C.sd_journal_get_realtime_usec(j, &stamp) != 0 {
					break
				} else {
					// Compare the timestamp on the entry
					// to our threshold value.
					if sinceUnixMicro != 0 && sinceUnixMicro > uint64(stamp) {
						break
					}
				}
				// Read the actual logged message.
				if C.get_message(j, &msg, &length) != -C.ENOENT {
					lines--
					log.Reset()
					// Recover the stream name by mapping
					// from the journal priority back to
					// the stream that we would have
					// assigned that value.
					if C.get_priority(j, &priority) != 0 {
						priority = 0
					} else if priority == C.int(journal.PriErr) {
						log.Stream = "stderr"
					} else if priority == C.int(journal.PriInfo) {
						log.Stream = "stdout"
					}
					// Set the time and text of the entry.
					log.Created = time.Unix(int64(stamp)/1000000, (int64(stamp)%1000000)*1000)
					log.Log = C.GoStringN(msg, C.int(length)) + "\n"
					// Buffer it so that we can send them
					// all in the right order later.
					j, err := log.Format("json")
					if err != nil {
						break
					}
					buffer = append(buffer, j)
				}
				// If we're at the start of the journal, or
				// don't need to find any more entries, stop.
				if lines == 0 || C.sd_journal_previous(j) <= 0 {
					break
				}
			}
			// Send the entries in the right order.
			for i := len(buffer) - 1; i >= 0; i-- {
				fmt.Fprintln(writer, buffer[i])
			}
		} else {
			// Start at the beginning of the journal.
			if C.sd_journal_seek_head(j) < 0 {
				return
			}
			// If we have a cutoff date, fast-forward to it.
			if sinceUnixMicro != 0 && C.sd_journal_seek_realtime_usec(j, C.uint64_t(sinceUnixMicro)) != 0 {
				return
			}
			if C.sd_journal_next(j) < 0 {
				return
			}
			for {
				// Read the actual logged message.
				if C.get_message(j, &msg, &length) != -C.ENOENT {
					// Read the entry's timestamp.
					if C.sd_journal_get_realtime_usec(j, &stamp) != 0 {
						break
					}
					log.Reset()
					// Recover the stream name by mapping
					// from the journal priority back to
					// the stream that we would have
					// assigned that value.
					if C.get_priority(j, &priority) != 0 {
						priority = 0
					} else if priority == C.int(journal.PriErr) {
						log.Stream = "stderr"
					} else if priority == C.int(journal.PriInfo) {
						log.Stream = "stdout"
					}
					// Set the time and text of the entry.
					log.Created = time.Unix(int64(stamp)/1000000, (int64(stamp)%1000000)*1000)
					log.Log = C.GoStringN(msg, C.int(length)) + "\n"
					// Dump the data to the pipe as fast as we can.
					j, err := log.Format("json")
					if err != nil {
						break
					}
					fmt.Fprintln(writer, j)
				}
				// If we're at the end of the journal, stop.
				if C.sd_journal_next(j) <= 0 {
					break
				}
			}
		}
	}()
	return reader, nil
}
