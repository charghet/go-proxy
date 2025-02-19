package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

var version = "0.4.1"

func handleConnection(clientConn net.Conn, remoteAddr string, f *Flag) {
	// defer fmt.Println("handleConnection end")
	defer func() {
		if r := recover(); r != nil {
			debug.PrintStack()
		}
	}()

	defer clientConn.Close()

	serverConn, err := net.Dial("tcp", remoteAddr)
	if err != nil {
		fmt.Printf("连接到远程服务器失败: %v\n", err)
		return
	}

	down := make(chan interface{}, 10)
	defer func() {
		down <- nil
	}()
	cr := make(chan []byte, 5)
	cw := make(chan []byte, 5)

	// client read
	go func() {
		defer func() {
			down <- nil
			cr <- nil
			// fmt.Println("client read end")
		}()
		for {
			b := make([]byte, 4096)
			n, err := clientConn.Read(b)
			// fmt.Println("client read", string(b), "====================")
			if err != nil {
				// fmt.Printf("client read error: %v\n", err)
				return
			}
			cr <- b[:n]
		}
	}()

	// client write
	go func() {
		defer func() {
			// fmt.Println("client write end")
		}()
		i := 0
		for {
			select {
			case b := <-cw:
				if b == nil {
					continue
				}
				_, err := clientConn.Write(b)
				if err != nil {
					fmt.Printf("client write error: %v\n", err)
					return
				}
				if i == 0 {
					fmt.Println(time.Now(), clientConn.RemoteAddr(), ":\n"+strings.Replace(string(b), "\r\n\r\n", "", 1), "\n=========================================")
				}
				i++

			case <-down:
				down <- nil
				return
			}
		}
	}()

	sr := make(chan []byte, 5)
	sw := make(chan []byte)
	serverDown := make(chan interface{}, 5)

	go serverRead(serverConn, sr, down, serverDown)
	go serverWrite(serverConn, sw, down, serverDown)

	// client read to server write
	var cb bytes.Buffer
	var tls []byte
	go func() {
		// defer fmt.Println("client read to server write end")
		i := 0
		wcb := true
		tls_index := -1
		for {
			var b []byte
			select {
			case b = <-cr:
			case <-down:
				down <- nil
				return
			}
			if i == 0 {
				fmt.Println(time.Now(), clientConn.RemoteAddr(), ":\n"+strings.Replace(string(b), "\r\n\r\n", "", 1), "\n=========================================")
			}
			if wcb {
				cb.Write(b)
			}
			if strings.Contains(string(b), "\r\n\r\n") {
				wcb = false
			}

			if tls_index == i && tls == nil {
				tls = b
			}
			if tls == nil && !wcb && tls_index == -1 {
				tls_index = i + 1
			}
			select {
			case sw <- b:
			case <-down:
				down <- nil
				return
			}
			if i < 10 {
				i++
			}
		}
	}()

	// server read to client write
	src := make(chan chan []byte, 5)
	i := 0
	t := 1
	var cbr *bytes.Reader = nil
	http := true
	var sb strings.Builder
	wsb := true
	write_buff := false
	write_head := false
	tls_index := -1
	tls_error := false
	reset := false
	for {
		var b []byte
		if reset {
			sr = <-src
			reset = false
		}
		// fmt.Println("sr:", &sr)
		select {
		case b = <-sr:
		case <-down:
			down <- nil
			return
		}
		// TODO b == nil 重试
		// fmt.Println("server read:", len(b), b == nil, "\n====================\n", s, "\n====================")
		if wsb {
			sb.Write(b)
			if strings.Contains(sb.String(), "\r\n\r\n") {
				wsb = false
				tls_index = i + 1
			}
		}
		if strings.HasPrefix(string(b), "HTTP/1.1 200 Connection established") {
			http = false
		}
		if http {
			if !write_head && !wsb {
				if strings.HasPrefix(sb.String(), "HTTP/1.1 503 Service Unavailable") && strings.Contains(sb.String(), "Proxy-Connection: close") {
					fmt.Println(time.Now(), clientConn.RemoteAddr(), "=====http_error=====", "try times:", t)
					if t == f.t {
						fmt.Println("try times:", t, "reach max try times")
						return
					}

					serverConn.Close()
					for {
						serverConn, err = net.Dial("tcp", remoteAddr)
						if err != nil {
							fmt.Println("连接到远程服务器失败:", err, "try times:", t)
							t++
							time.Sleep(time.Millisecond * 300)
						}
						if err == nil {
							break
						}
						if t == f.t {
							fmt.Println("try times:", t, "reach max try times")
							return
						}
					}
					if cbr == nil {
						cbr = bytes.NewReader(cb.Bytes())
					}
					serverDown = make(chan interface{}, 5)
					sr := make(chan []byte, 5)
					src <- sr
					go serverRead(serverConn, sr, down, serverDown)
					go serverWrite(serverConn, sw, down, serverDown)

					buff, err := io.ReadAll(cbr)
					if err != nil {
						fmt.Println("read error:", err)
						return
					}
					cbr.Seek(0, io.SeekStart)
					sw <- buff
					write_buff = true

					i = 0
					t++
					sb.Reset()
					wsb = true
					write_head = false
					reset = true
					continue
				}
			}
		} else { // https
			if tls_index == i && b == nil {
				fmt.Println(time.Now(), clientConn.RemoteAddr(), "=====tls_error=====", "try times:", t)
				tls_error = true
				if t == f.t {
					fmt.Println("try times:", t, "reach max try times")
					return
				}

				serverConn.Close()
				for {
					serverConn, err = net.Dial("tcp", remoteAddr)
					if err != nil {
						fmt.Println("连接到远程服务器失败:", err, "try times:", t)
						t++
						time.Sleep(time.Millisecond * 300)
					}
					if err == nil {
						break
					}
					if t == f.t {
						fmt.Println("try times:", t, "reach max try times")
						return
					}
				}

				if cbr == nil {
					cbr = bytes.NewReader(cb.Bytes())
				}
				serverDown = make(chan interface{}, 5)
				sr = make(chan []byte, 5)
				src <- sr
				go serverRead(serverConn, sr, down, serverDown)
				go serverWrite(serverConn, sw, down, serverDown)

				buff, err := io.ReadAll(cbr)
				if err != nil {
					fmt.Println("read error:", err)
					return
				}
				cbr.Seek(0, io.SeekStart)
				sw <- buff
				sw <- tls
				write_buff = true

				i = 0
				t++
				sb.Reset()
				wsb = true
				write_head = false
				tls_index = -1
				reset = true
				continue
			}
		}
		if write_buff {
			write_buff = false
		} else {
			if !write_head && !wsb && !tls_error {
				cw <- []byte(sb.String())
				// fmt.Println("write_head", sb.String())
				write_head = true
			} else if !http || !wsb {
				cw <- b
				// fmt.Println("write_buff", string(b))
			}
		}
		if b == nil {
			break
		}
		if i < 10 {
			i++
		}
	}

}

func serverRead(serverConn net.Conn, sr chan []byte, down chan interface{}, serverDown chan interface{}) {
	defer func() {
		serverDown <- nil
		// fmt.Println("server read end")
	}()
	i := 0
	for {
		b := make([]byte, 4096)
		// serverConn.SetReadDeadline(time.Now().Add(time.Duration(t) * time.Second))
		n, err := serverConn.Read(b)
		if err != nil {
			// fmt.Println("server read error:", serverConn.LocalAddr(), err)
			sr <- nil
			return
		}
		// fmt.Println("server read", n, string(b[:n]))
		select {
		case sr <- b[:n]:
		case <-down:
			down <- nil
			return
		case <-serverDown:
			return
		}
		i++
	}
}

func serverWrite(serverConn net.Conn, sw <-chan []byte, down chan interface{}, serverDown chan interface{}) {
	defer func() {
		serverConn.Close()
		serverDown <- nil
		// fmt.Println("server write end")
	}()
	for {
		select {
		case b := <-sw:
			_, err := serverConn.Write(b)
			if err != nil {
				// fmt.Printf("server write error: %v %v\n", serverConn.LocalAddr(), err)
				return
			}
		case <-down:
			down <- nil
			return
		case <-serverDown:
			return
		}
	}
}

type Flag struct {
	b string // bind address. like localhost:10170
	p string // proxy address. like localhost:20170
	v bool   // version
	h bool   // help
	t int    // try times
}

func getArgs() *Flag {
	var f Flag
	flag.StringVar(&f.b, "b", "0.0.0.0:10171", "bind address.")
	flag.StringVar(&f.p, "p", "localhost:20171", "proxy address.")
	flag.BoolVar(&f.v, "v", false, "print version.")
	flag.BoolVar(&f.h, "h", false, "show help.")
	flag.IntVar(&f.t, "t", 10, "try times.")
	flag.Usage = func() {
		fmt.Println("forword http proxy server and retry request to improved stability")
		fmt.Fprintf(os.Stdout, "Usage:  %s [options] [path]\n", os.Args[0])
		fmt.Printf("Version: %s\n", version)
		fmt.Println("Options:")
		flag.PrintDefaults()
	}

	flag.Parse()

	if f.v {
		fmt.Println("version:", version)
		os.Exit(0)
	}

	if f.h {
		flag.Usage()
		os.Exit(0)
	}

	return &f
}

func main() {
	f := getArgs()
	localAddr := f.b
	remoteAddr := f.p

	listener, err := net.Listen("tcp", localAddr)
	if err != nil {
		fmt.Printf("监听失败: %v\n", err)
		return
	}
	defer listener.Close()

	fmt.Printf("listening %s and forword to %s\n", localAddr, remoteAddr)

	for {
		clientConn, err := listener.Accept()
		if err != nil {
			fmt.Printf("接受连接失败: %v\n", err)
			continue
		}

		go handleConnection(clientConn, remoteAddr, f)
	}
}
