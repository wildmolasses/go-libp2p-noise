package noise

import (
	"context"
	"fmt"
	log "github.com/ChainSafe/log15"
	proto "github.com/gogo/protobuf/proto"
	//"github.com/libp2p/go-libp2p-core/peer"

	ik "github.com/ChainSafe/go-libp2p-noise/ik"
	pb "github.com/ChainSafe/go-libp2p-noise/pb"
)

func (s *secureSession) ik_sendHandshakeMessage(payload []byte) error {
	log.Debug("ik_sendHandshakeMessage", "initiator", s.initiator, "payload", payload, "payload len", len(payload))

	// create send message w payload
	var msgbuf ik.MessageBuffer
	s.ik_ns, msgbuf = ik.SendMessage(s.ik_ns, payload)
	var encMsgBuf []byte
	//if initial_stage {
	encMsgBuf = msgbuf.Encode0()
	// } else {
	// 	encMsgBuf = msgbuf.Encode1()
	// }

	log.Debug("ik_sendHandshakeMessage", "initiator", s.initiator, "msgbuf", msgbuf)
	log.Debug("ik_sendHandshakeMessage", "initiator", s.initiator, "encMsgBuf", encMsgBuf, "ns_len", len(msgbuf.NS()), "enc_len", len(encMsgBuf))

	err := s.WriteLength(len(encMsgBuf))
	if err != nil {
		log.Error("xx_sendHandshakeMessage", "initiator", s.initiator, "error", err)
		return fmt.Errorf("xx_sendHandshakeMessage write length fail: %s", err)
	}

	// send message
	_, err = s.insecure.Write(encMsgBuf)
	if err != nil {
		log.Error("ik_sendHandshakeMessage", "initiator", s.initiator, "error", err)
		return fmt.Errorf("write to conn fail: %s", err)
	}

	return nil
}

func (s *secureSession) ik_recvHandshakeMessage() (buf []byte, plaintext []byte, valid bool, err error) {
	l, err := s.ReadLength()
	if err != nil {
		return nil, nil, false, fmt.Errorf("read length fail: %s", err)
	}

	buf = make([]byte, l)

	_, err = s.insecure.Read(buf)
	if err != nil {
		return buf, nil, false, fmt.Errorf("read from conn fail: %s", err)
	}

	var msgbuf *ik.MessageBuffer
	//if initial_stage {
	msgbuf, err = ik.Decode0(buf)
	// } else {
	// 	msgbuf, err = ik.Decode1(buf)
	// }

	log.Debug("ik_recvHandshakeMessage", "initiator", s.initiator, "msgbuf", msgbuf, "buf len", len(buf))

	if err != nil {
		log.Error("ik_recvHandshakeMessage decode", "initiator", s.initiator, "error", err)
		return buf, nil, false, fmt.Errorf("decode msg fail: %s", err)
	}

	s.ik_ns, plaintext, valid = ik.RecvMessage(s.ik_ns, msgbuf)
	if !valid {
		log.Error("ik_recvHandshakeMessage", "initiator", s.initiator, "error", "validation fail")
		return buf, nil, false, fmt.Errorf("validation fail")
	}

	log.Debug("recv handshake message", "initiator", s.initiator, "msgbuf", msgbuf, "payload len", len(plaintext))

	return buf, plaintext, valid, nil
}

func (s *secureSession) runHandshake_ik(ctx context.Context, handshakeData []byte) error {
	// generate local static noise key
	kp := ik.GenerateKeypair()

	log.Debug("ik handshake", "pubkey", kp.PubKey())

	// setup libp2p keys
	localKeyRaw, err := s.LocalPublicKey().Bytes()
	if err != nil {
		return fmt.Errorf("err getting raw pubkey: %s", err)
	}

	log.Debug("ik handshake", "local key", localKeyRaw, "len", len(localKeyRaw))

	// sign noise data for payload
	noise_pub := kp.PubKey()
	signedPayload, err := s.localKey.Sign(append([]byte(payload_string), noise_pub[:]...))
	if err != nil {
		return fmt.Errorf("err signing payload: %s", err)
	}

	// create payload
	payload := new(pb.NoiseHandshakePayload)
	payload.Libp2PKey = localKeyRaw
	payload.NoiseStaticKeySignature = signedPayload
	payloadEnc, err := proto.Marshal(payload)
	if err != nil {
		return fmt.Errorf("proto marshal payload fail: %s", err)
	}

	// new XX noise session
	s.ik_ns = ik.InitSession(s.initiator, s.prologue, kp, s.noiseStaticKeyCache[s.remotePeer])
	log.Debug("ik initiator init session", "remotePeer", s.noiseStaticKeyCache[s.remotePeer])
	
	if s.initiator {
		// stage 0 //
		err := s.ik_sendHandshakeMessage(payloadEnc)
		if err != nil {
			log.Error("stage 0 initiator send", "err", err)
			return fmt.Errorf("stage 0 initiator fail: %s", err)
		}

		// stage 1 //

	} else {
		// stage 0 //

		var plaintext []byte
		var valid bool

		if handshakeData != nil {
			var msgbuf *ik.MessageBuffer
			msgbuf, err = ik.Decode0(handshakeData)

			log.Debug("stage 0 ik_recvHandshakeMessage", "initiator", s.initiator, "msgbuf", msgbuf, "buf len", len(handshakeData))

			if err != nil {
				return fmt.Errorf("stage 0 responder fail: %s", err)
			}

			s.ik_ns, plaintext, valid = ik.RecvMessage(s.ik_ns, msgbuf)
		} else {
			// read message
			_, plaintext, valid, err = s.ik_recvHandshakeMessage()
			if err != nil {
				return fmt.Errorf("stage 0 responder fail: %s", err)
			}

		}

		if !valid {
			return fmt.Errorf("stage 0 responder validation fail")
		}

		log.Debug("stage 0 responder", "plaintext", plaintext, "plaintext len", len(plaintext))

		// unmarshal payload
		nhp := new(pb.NoiseHandshakePayload)
		err = proto.Unmarshal(plaintext, nhp)
		if err != nil {
			return fmt.Errorf("stage 0 responder validation fail: cannot unmarshal payload")
		}

		// set remote libp2p public key
		err = s.setRemotePeerInfo(nhp.GetLibp2PKey())
		if err != nil {
			log.Error("stage 0 responder set remote peer info", "err", err)
			return fmt.Errorf("stage 0 responder read remote libp2p key fail")
		}

		// assert that remote peer ID matches libp2p key
		err = s.setRemotePeerID(s.RemotePublicKey())
		if err != nil {
			log.Error("stage 0 responder set remote peer id", "err", err)
		}

		// verify payload is signed by libp2p key
		err = s.verifyPayload(nhp, s.ik_ns.RemoteKey())
		if err != nil {
			log.Error("stage 1 responder verify payload", "err", err)
		}

	}

	return nil
}