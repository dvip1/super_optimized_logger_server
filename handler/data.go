package handler

import (
	"encoding/binary"
	"io"
	"net/http"
)

// Packet layout (all little-endian), see the challenge spec:
//
//	[0]       version (1)
//	[1:9]     srno, 8 raw bytes
//	[9:21]    nonce, 12 bytes
//	[21:301]  AES-128-GCM ciphertext (264) + tag (16), AAD = [0:9]
//

// Plaintext: [0:8] timestamp int64, [8:264] 64 x float32.
const (
	packetSize  = 301
	aadEnd      = 9  // version + srno are authenticated, not encrypted
	nonceEnd    = 21 // header (version + srno + nonce) ends here
	sensorBytes = 256
)

func IngestData(w http.ResponseWriter, r *http.Request) {
	//start(request-check): checking packet size (it's not long nor short, that's why +1)
	var buf [packetSize + 1]byte
	n, _ := io.ReadFull(r.Body, buf[:])
	packet := buf[:n]
	if n != packetSize || packet[0] != 1 {
		writeError(w, http.StatusBadRequest, "bad packet size or version")
		return
	}
	//end

	//start(request-check): loooking up device
	d := lookupDevice([8]byte(packet[1:aadEnd]))
	if d == nil {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	//end

	// The device lock makes decrypt + replay check + lastTs update one step:
	// a re-registration can't swap the key midway, and two packets of the same
	// device can't both pass the replay check.
	d.mu.Lock()
	if d.aead == nil || isRevoked(d.tokenID) {
		d.mu.Unlock()
		writeError(w, http.StatusForbidden, "device not registered or token revoked")
		return
	}
	// Open verifies the tag (covering ciphertext and AAD) before returning
	// anything; it decrypts in place into the ciphertext's own bytes.
	plain, err := d.aead.Open(packet[nonceEnd:nonceEnd], packet[aadEnd:nonceEnd], packet[nonceEnd:], packet[:aadEnd])
	if err != nil {
		d.mu.Unlock()
		writeError(w, http.StatusBadRequest, "packet does not decrypt with the device's key")
		return
	}
	ts := int64(binary.LittleEndian.Uint64(plain[:8]))
	if ts <= d.lastTs {
		d.mu.Unlock()
		writeError(w, http.StatusConflict, "timestamp not newer than the last reading (replay)")
		return
	}
	d.lastTs = ts // only now: rejected packets never move lastTs

	// Hand the reading to the writer, still under the lock: this device's
	// readings then reach the writer (and its buffer) in timestamp order.
	done := make(chan error, 1)
	writeQueue <- pendingRow{dev: d, srno: packet[1:aadEnd], ts: ts, sensors: plain[8 : 8+sensorBytes], done: done}
	d.mu.Unlock()
	// Wait until the reading is written to the segment file.
	if err := <-done; err != nil {
		writeError(w, http.StatusInternalServerError, "could not store reading")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
