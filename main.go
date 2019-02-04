package main

import (
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var activeConnections = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "connections_active",
	Help: "Number of active WS connections",
})

var backendConnections = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "connections_total",
	Help: "Total number of connections",
}, []string{"result"})

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	Subprotocols:    []string{"binary"},
}

func init() {
	prometheus.MustRegister(activeConnections)
	prometheus.MustRegister(backendConnections)
}

func main() {
	path := os.Getenv("HTTP_PATH")
	if path == "" {
		path = "/"
	}
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc(path, handler)
	http.ListenAndServe(":8023", nil)
}

func handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()
	var tcpConn net.Conn
	if os.Getenv("TLS_TARGET") != "" {
		tcpConn, err = tls.Dial("tcp", os.Getenv("REMOTE_ADDR"), &tls.Config{
			InsecureSkipVerify: os.Getenv("NO_SSL_VERIFY") != "",
		})
	} else {
		tcpConn, err = net.Dial("tcp", os.Getenv("REMOTE_ADDR"))
	}
	if err != nil {
		log.Println(err)
		backendConnections.WithLabelValues("failed").Inc()
		return
	}
	backendConnections.WithLabelValues("successful").Inc()
	activeConnections.Inc()
	proxy(conn, tcpConn)
}

func proxy(wsConn *websocket.Conn, tcpConn net.Conn) {
	defer activeConnections.Dec()
	go func() {
		defer wsConn.Close()
		defer tcpConn.Close()
		for {
			t, buf, err := wsConn.ReadMessage()
			if err != nil {
				log.Println(err)
				return
			}
			switch t {
			case websocket.BinaryMessage:
				_, err := tcpConn.Write(buf)
				if err != nil {
					log.Println(err)
					return
				}
			case websocket.PingMessage:
				if err := wsConn.WriteMessage(websocket.PongMessage, buf); err != nil {
					log.Println(err)
					return
				}
			case websocket.PongMessage:
			case websocket.TextMessage:
			default:
			}
		}
	}()
	defer wsConn.Close()
	defer tcpConn.Close()
	buf := make([]byte, 4096)
	for {
		n, err := tcpConn.Read(buf)
		if err != nil {
			log.Println(err)
			return
		}
		if err := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
			log.Println(err)
			return
		}
	}
}
