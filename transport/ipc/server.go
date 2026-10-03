package ipc

import (
	"errors"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/p1neappleXpress/OpenFlux/utils"
)

// Handler receives decoded messages from the connected client.
type Handler interface {
	OnCommand(p *CommandPayload)
	OnCookies(p *CookiesOfferPayload)
	OnConnect()
	OnDisconnect()
}

// Server is a single-client Unix domain socket server.
type Server struct {
	path    string
	handler Handler

	mu              sync.Mutex
	listener        net.Listener
	conn            net.Conn
	writeMu         sync.Mutex
	closed          bool
	pending         []pendingFrame
	requestSequence uint64
}

type pendingFrame struct {
	typ     MsgType
	payload interface{}
}

func NewServer(path string, h Handler) *Server {
	return &Server{path: path, handler: h}
}

// Listen removes a stale socket file if present and binds a fresh one.
func (s *Server) Listen() error {
	if s.path == "" {
		return errors.New("ipc: empty path")
	}
	if _, err := os.Stat(s.path); err == nil {
		_ = os.Remove(s.path)
	}
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}
	// A core run as root still serves the app that started it.
	utils.GiveToDirOwner(s.path)
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	utils.Debugf("[IPC] listening on %s", s.path)
	go s.acceptLoop()
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			utils.Debugf("[IPC] accept: %v", err)
			continue
		}
		s.writeMu.Lock()
		s.mu.Lock()
		if s.conn != nil {
			_ = s.conn.Close()
		}
		s.conn = conn
		pending := append([]pendingFrame(nil), s.pending...)
		s.pending = nil
		s.mu.Unlock()
		for _, frame := range pending {
			if err := s.write(conn, frame.typ, frame.payload); err != nil {
				utils.Debugf("[IPC] replay pending frame: %v", err)
				break
			}
		}
		s.writeMu.Unlock()
		if s.handler != nil {
			s.handler.OnConnect()
		}
		go s.serve(conn)
	}
}

func (s *Server) serve(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.mu.Unlock()
		_ = conn.Close()
		if s.handler != nil {
			s.handler.OnDisconnect()
		}
	}()

	for {
		typ, payload, err := ReadFrame(conn)
		if err != nil {
			return
		}
		switch typ {
		case MsgCommand:
			var p CommandPayload
			if err := DecodeJSON(payload, &p); err != nil {
				utils.Debugf("[IPC] bad Command: %v", err)
				continue
			}
			if s.handler != nil {
				s.handler.OnCommand(&p)
			}
		case MsgCookiesOffer:
			var p CookiesOfferPayload
			if err := DecodeJSON(payload, &p); err != nil {
				utils.Debugf("[IPC] bad CookiesOffer: %v", err)
				continue
			}
			if s.handler != nil {
				s.handler.OnCookies(&p)
			}
		default:
			utils.Debugf("[IPC] unexpected type 0x%02x from app", typ)
		}
	}
}

// Send writes a frame to the connected client. If no client is connected,
// the frame is dropped.
func (s *Server) Send(typ MsgType, payload interface{}) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	conn := s.conn
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if conn == nil && typ == MsgCookiesRequest && !s.closed {
		if len(s.pending) >= 8 {
			s.pending = s.pending[1:]
		}
		s.pending = append(s.pending, pendingFrame{typ: typ, payload: payload})
	}
	s.mu.Unlock()
	if conn == nil {
		if typ == MsgCookiesRequest {
			return nil
		}
		return errors.New("ipc: no client connected")
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return WriteFrame(conn, typ, payload)
}

func (s *Server) write(conn net.Conn, typ MsgType, payload interface{}) error {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return WriteFrame(conn, typ, payload)
}

func (s *Server) SendCookiesRequest(p *CookiesRequestPayload) error {
	if p == nil {
		return errors.New("ipc: nil cookies request")
	}
	request := *p
	s.mu.Lock()
	if request.RequestID == "" {
		s.requestSequence++
		request.RequestID = strconv.FormatUint(s.requestSequence, 10)
	}
	s.mu.Unlock()
	return s.Send(MsgCookiesRequest, &request)
}

func (s *Server) SendStatus(p *StatusPayload) error {
	return s.Send(MsgStatus, p)
}

func (s *Server) SendLog(line string) error {
	return s.Send(MsgLog, &LogPayload{Line: line})
}

// Close stops the server and removes the socket file.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.listener
	conn := s.conn
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	if conn != nil {
		_ = conn.Close()
	}
	if s.path != "" {
		_ = os.Remove(s.path)
	}
	return nil
}
