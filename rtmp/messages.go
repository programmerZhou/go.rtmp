// The MIT License (MIT)
//
// Copyright (c) 2014 winlin
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
// the Software, and to permit persons to whom the Software is furnished to do so,
// subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
// FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
// COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
// IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
// CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

package rtmp

import (
	"net"
	"math/rand"
	"time"
)

/**
* the rtmp message, encode/decode to/from the rtmp stream,
* which contains a message header and a bytes payload.
* the header is RtmpMessageHeader, where the payload canbe decoded by RtmpPacket.
*/
// @see: ISrsMessage, SrsCommonMessage, SrsSharedPtrMessage
type RtmpMessage struct {
	// 4.1. Message Header
	Header *RtmpMessageHeader
	// 4.2. Message Payload
	/**
	* The other part which is the payload is the actual data that is
	* contained in the message. For example, it could be some audio samples
	* or compressed video data. The payload format and interpretation are
	* beyond the scope of this document.
	*/
	Payload []byte
	/**
	* the payload is received from connection,
	* when len(Payload) == ReceivedPayloadLength, message receive completed.
	 */
	ReceivedPayloadLength int
	/**
	* get the perfered cid(chunk stream id) which sendout over.
	* set at decoding, and canbe used for directly send message,
	* for example, dispatch to all connections.
	* @see: SrsSharedPtrMessage.SrsSharedPtr.perfer_cid
	*/
	PerferCid int
	/**
	* the payload sent length.
	 */
	SentPayloadLength int
}

/**
* incoming chunk stream maybe interlaced,
* use the chunk stream to cache the input RTMP chunk streams.
*/
type RtmpChunkStream struct {
	/**
	* represents the basic header fmt,
	* which used to identify the variant message header type.
	*/
	FMT byte
	/**
	* represents the basic header cid,
	* which is the chunk stream id.
	*/
	CId int
	/**
	* cached message header
	*/
	Header *RtmpMessageHeader
	/**
	* whether the chunk message header has extended timestamp.
	*/
	ExtendedTimestamp bool
	/**
	* partially read message.
	*/
	Msg *RtmpMessage
	/**
	* decoded msg count, to identify whether the chunk stream is fresh.
	*/
	MsgCount int64
}
func NewRtmpChunkStream(cid int) (r *RtmpChunkStream) {
	r = &RtmpChunkStream{}

	r.CId = cid
	r.Header = &RtmpMessageHeader{}

	return
}

/**
* the message header for RtmpMessage,
* the header can be used in chunk stream cache, for the chunk stream header.
* @see: RTMP 4.1. Message Header
*/
type RtmpMessageHeader struct {
	/**
	* One byte field to represent the message type. A range of type IDs
	* (1-7) are reserved for protocol control messages.
	*/
	MessageType byte
	/**
	* Three-byte field that represents the size of the payload in bytes.
	* It is set in big-endian format.
	*/
	PayloadLength uint32
	/**
	* Three-byte field that contains a timestamp delta of the message.
	* The 3 bytes are packed in the big-endian order.
	* @remark, only used for decoding message from chunk stream.
	*/
	TimestampDelta uint32
	/**
	* Four-byte field that identifies the stream of the message. These
	* bytes are set in little-endian format.
	*/
	StreamId uint32

	/**
	* Four-byte field that contains a timestamp of the message.
	* The 4 bytes are packed in the big-endian order.
	* @remark, used as calc timestamp when decode and encode time.
	* @remark, we use 64bits for large time for jitter detect and hls.
	*/
	Timestamp uint64
}

/**
* the handshake data, 6146B = 6KB,
* store in the protocol and never delete it for every connection.
 */
type RtmpHandshake struct {
	c0c1 []byte // 1537B
	s0s1s2 []byte // 3073B
	c2 []byte // 1536B
}

type RtmpAckWindowSize struct {
	ack_window_size uint32
	acked_size uint64
}

type RtmpProtocol interface {
	/**
	* do simple handshake with client, user can try simple/complex interlace,
	* that is, try complex handshake first, use simple if complex handshake failed.
	 */
	SimpleHandshake2Client() (err error)
	/**
	* recv message from connection.
	* the payload of message is []byte, user can decode it by DecodeMessage.
	 */
	//RecvMessage() (msg *RtmpMessage, err error)
	/**
	* decode the received message to pkt.
	 */
	//DecodeMessage(msg *RtmpMessage) (pkt interface {}, err error)
	/**
	* expect specified message by v, where v must be a ptr,
	* protocol stack will RecvMessage from connection and convert/set msg to v
	* if type matched, or drop the message and try again.
	 */
	ExpectMessage(v interface {}) (msg *RtmpMessage, err error)
	/**
	* encode the packet to message, then send out by SendMessage.
	* return the cid which packet prefer.
	 */
	//EncodeMessage(pkt RtmpEncoder) (cid int, msg *RtmpMessage, err error)
	/**
	* send message to peer over rtmp connection.
	* ignore and use default header if param header is nil.
	* if pkt is RtmpEncoder, encode the pkt to RtmpMessage and send out.
	* if pkt is RtmpMessage already, directly send it out.
	 */
	SendMessage(pkt interface {}, header *RtmpMessageHeader) (err error)
}
/**
* create the rtmp protocol.
 */
func NewRtmpProtocol(conn *net.TCPConn) (RtmpProtocol, error) {
	r := &rtmpProtocol{}

	r.conn = NewRtmpSocket(conn)
	r.chunkStreams = map[int]*RtmpChunkStream{}
	r.buffer = NewRtmpBuffer(r.conn)
	r.handshake = &RtmpHandshake{}

	r.inChunkSize = RTMP_DEFAULT_CHUNK_SIZE
	r.outChunkSize = r.inChunkSize
	r.outHeaderFmt0 = make([]byte, RTMP_MAX_FMT0_HEADER_SIZE)
	r.outHeaderFmt3 = make([]byte, RTMP_MAX_FMT3_HEADER_SIZE)

	rand.Seed(time.Now().UnixNano())

	return r, nil
}

/**
* max rtmp header size:
* 	1bytes basic header,
* 	11bytes message header,
* 	4bytes timestamp header,
* that is, 1+11+4=16bytes.
*/
const RTMP_MAX_FMT0_HEADER_SIZE = 16
/**
* max rtmp header size:
* 	1bytes basic header,
* 	4bytes timestamp header,
* that is, 1+4=5bytes.
*/
const RTMP_MAX_FMT3_HEADER_SIZE = 5

/**
* the protocol provides the rtmp-message-protocol services,
* to recv RTMP message from RTMP chunk stream,
* and to send out RTMP message over RTMP chunk stream.
*/
type rtmpProtocol struct {
// handshake
	handshake *RtmpHandshake
// peer in/out
	// the underlayer tcp connection, to read/write bytes from/to.
	conn RtmpSocket
// peer in
	chunkStreams map[int]*RtmpChunkStream
	// the bytes read from underlayer tcp connection,
	// used for parse to RTMP message or packets.
	buffer RtmpBuffer
	// input chunk stream chunk size.
	inChunkSize int32
	// the acked size
	inAckSize RtmpAckWindowSize
// peer out
	// output chunk stream chunk size.
	outChunkSize int32
	// bytes cache, size is RTMP_MAX_FMT0_HEADER_SIZE
	outHeaderFmt0 []byte
	// bytes cache, size is RTMP_MAX_FMT3_HEADER_SIZE
	outHeaderFmt3 []byte
}

/**
* the payload codec by the RtmpPacket.
* @see: RTMP 4.2. Message Payload
*/
// @see: SrsPacket
/**
* the decoded message payload.
* @remark we seperate the packet from message,
*		for the packet focus on logic and domain data,
*		the message bind to the protocol and focus on protocol, such as header.
* 		we can merge the message and packet, using OOAD hierachy, packet extends from message,
* 		it's better for me to use components -- the message use the packet as payload.
*/
type RtmpDecoder interface {
	/**
	* decode the packet from the s, which is created by rtmp message.
	 */
	Decode(s RtmpStream) (err error)
}
/**
* encode the rtmp packet to payload of rtmp message.
 */
type RtmpEncoder interface {
	/**
	* get the rtmp chunk cid the packet perfered.
	 */
	GetPerferCid() (v int)
	/**
	* get the size of packet, to create the RtmpStream.
	 */
	GetSize() (v int)
	/**
	* encode the packet to s, which is created by size=GetSize()
	 */
	Encode(s RtmpStream) (err error)
}
func DecodeRtmpPacket(r RtmpProtocol, header *RtmpMessageHeader, payload []byte) (packet interface {}, err error) {
	var pkt RtmpDecoder= nil
	var stream RtmpStream = NewRtmpStream(payload)

	// decode specified packet type
	if header.IsAmf0Command() || header.IsAmf3Command() || header.IsAmf0Data() || header.IsAmf3Data() {
		// skip 1bytes to decode the amf3 command.
		if header.IsAmf3Command() &&  stream.Requires(1) {
			stream.Next(1)
		}

		amf0_codec := NewRtmpAmf0Codec(stream)

		// amf0 command message.
		// need to read the command name.
		var command string
		if command, err = amf0_codec.ReadString(); err != nil {
			return
		}

		// result/error packet
		if command == RTMP_AMF0_COMMAND_RESULT || command == RTMP_AMF0_COMMAND_ERROR {
			// TODO: FIXME: implements it
		}

		// reset to zero(amf3 to 1) to restart decode.
		if header.IsAmf3Command() &&  stream.Requires(1) {
			stream.Reset(1)
		} else {
			stream.Reset(0)
		}

		// decode command object.
		if command == RTMP_AMF0_COMMAND_CONNECT {
			pkt = &RtmpConnectAppPacket{}
		}
		// TODO: FIXME: implements it
	} else if header.IsWindowAcknowledgementSize() {
		pkt = &RtmpSetWindowAckSizePacket{}
	}
	// TODO: FIXME: implements it

	if err == nil && pkt != nil {
		packet, err = pkt, pkt.Decode(stream)
	}

	return
}

/**
* 4.1.1. connect
* The client sends the connect command to the server to request
* connection to a server application instance.
*/
// @see: SrsConnectAppPacket
type RtmpConnectAppPacket struct {
	CommandName string
	TransactionId float64
	CommandObject *RtmpAmf0Object
}
func (r *RtmpConnectAppPacket) Decode(s RtmpStream) (err error) {
	codec := NewRtmpAmf0Codec(s)

	if r.CommandName, err = codec.ReadString(); err != nil {
		return
	}
	if r.CommandName != RTMP_AMF0_COMMAND_CONNECT {
		err = RtmpError{code:ERROR_RTMP_AMF0_DECODE, desc:"amf0 decode connect command_name failed."}
		return
	}

	if r.TransactionId, err = codec.ReadNumber(); err != nil {
		return
	}
	if r.TransactionId != 1.0 {
		err = RtmpError{code:ERROR_RTMP_AMF0_DECODE, desc:"amf0 decode connect transaction_id failed."}
		return
	}

	if r.CommandObject, err = codec.ReadObject(); err != nil {
		return
	}
	if r.CommandObject == nil {
		err = RtmpError{code:ERROR_RTMP_AMF0_DECODE, desc:"amf0 decode connect command_object failed."}
		return
	}

	return
}

/**
* 5.5. Window Acknowledgement Size (5)
* The client or the server sends this message to inform the peer which
* window size to use when sending acknowledgment.
*/
// @see: SrsSetWindowAckSizePacket
type RtmpSetWindowAckSizePacket struct {
	AcknowledgementWindowSize uint32
}
func (r *RtmpSetWindowAckSizePacket) Decode(s RtmpStream) (err error) {
	if !s.Requires(4) {
		err = RtmpError{code:ERROR_RTMP_MESSAGE_DECODE, desc:"decode ack window size failed."}
		return
	}
	r.AcknowledgementWindowSize = s.ReadUInt32()
	return
}
func (r *RtmpSetWindowAckSizePacket) GetPerferCid() (v int) {
	return RTMP_CID_ProtocolControl
}
func (r *RtmpSetWindowAckSizePacket) GetSize() (v int) {
	return 4
}
func (r *RtmpSetWindowAckSizePacket) Encode(s RtmpStream) (err error) {
	if !s.Requires(4) {
		return RtmpError{code:ERROR_RTMP_MESSAGE_ENCODE, desc:"encode ack size packet failed."}
	}
	s.WriteUInt32(r.AcknowledgementWindowSize)
	return
}
