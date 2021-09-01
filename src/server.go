package main

import (
	"flag"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"sync"
	"text/template"
	"time"
)

func homeHandler(tpl *template.Template) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tpl.Execute(w, r)
	})
}

func main() {
	flag.Parse()
	tpl := template.Must(template.ParseFiles("index.html"))
	h := newHub()
	router := http.NewServeMux()
	router.Handle("/", homeHandler(tpl))
	router.Handle("/ws", wsHandler{h: h})
	log.Printf("serving on port 8080")
	log.Fatal(http.ListenAndServe(":8080", router))
}

type hub struct {
	// the mutex to protect connections
	connectionsMx sync.RWMutex

	// Registered connections.
	connections map[*connection]struct{}

	// 연결된 메세지 connections.
	broadcast   chan []byte
	setUserName string
	logMx       sync.RWMutex
	log         [][]byte
}

func newHub() *hub {
	h := &hub{
		connectionsMx: sync.RWMutex{},
		broadcast:     make(chan []byte),
		connections:   make(map[*connection]struct{}),
	}

	go func() {
		for {
			// 채팅방 메세지 가져옴
			msg := <-h.broadcast

			// 채팅방 잠금
			h.connectionsMx.RLock()
			for c := range h.connections {
				select {
				// 메세지가 들어왓을 경우
				case c.send <- msg:
				case <-time.After(1 * time.Second):
					log.Printf("shutting down connection %s", c)
					h.removeConnection(c)
				}
			}
			// 잠금해제
			h.connectionsMx.RUnlock()
		}
	}()
	return h
}

func (h *hub) addConnection(conn *connection) {
	h.connectionsMx.Lock()
	defer h.connectionsMx.Unlock()
	h.connections[conn] = struct{}{}
}

func (h *hub) removeConnection(conn *connection) {
	h.connectionsMx.Lock()
	defer h.connectionsMx.Unlock()
	if _, ok := h.connections[conn]; ok {
		delete(h.connections, conn)
		close(conn.send)
	}
}

type connection struct {
	// 메시지의 버퍼링된 채널입니다.
	send chan []byte

	// The hub.
	h *hub
}

func (c *connection) reader(wg *sync.WaitGroup, wsConn *websocket.Conn) {
	defer wg.Done()
	for {
		_, message, err := wsConn.ReadMessage()
		if err != nil {
			break
		}
		c.h.broadcast <- message
	}
}

func (c *connection) writer(wg *sync.WaitGroup, wsConn *websocket.Conn) {
	defer wg.Done()
	for message := range c.send {
		err := wsConn.WriteMessage(websocket.TextMessage, message)
		if err != nil {
			break
		}
	}
}

var upgrader = &websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024}

type wsHandler struct {
	h *hub
}

func (wsh wsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	wsConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("error upgrading %s", err)
		return
	}
	c := &connection{send: make(chan []byte, 256), h: wsh.h}
	c.h.addConnection(c)
	defer c.h.removeConnection(c)
	var wg sync.WaitGroup
	wg.Add(2)
	go c.writer(&wg, wsConn)
	go c.reader(&wg, wsConn)
	wg.Wait()
	wsConn.Close()
}
