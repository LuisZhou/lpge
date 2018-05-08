// Pacakge chanrpc provides base communication between service/module in the same process.
package chanrpc

import (
	"errors"
	"fmt"
	"github.com/LuisZhou/lpge/conf"
	"github.com/LuisZhou/lpge/log"
	"runtime"
	"time"
)

// RpcCommon is common field shared by rpc server and client.
type RpcCommon struct {
	timeout     time.Duration
	SkipCounter int
}

// CallInfo is info of call to server.
type CallInfo struct {
	id      interface{}
	args    []interface{}
	chanRet chan *RetInfo
	cb      func(interface{}, error)
}

// RetInfo is info of return from server.
type RetInfo struct {
	ret interface{}
	err error
	cb  func(interface{}, error)
}

// Server is a chanrpc server.
type Server struct {
	functions map[interface{}]interface{}
	ChanCall  chan *CallInfo
	RpcCommon
}

// Client is a chanrpc client.
type Client struct {
	chanSyncRet     chan *RetInfo
	ChanAsynRet     chan *RetInfo
	pendingAsynCall int
	AllowOverFlood  bool
	RpcCommon
}

// NewServer new a server. bufsize define the buffer size of call channel, timeout define max waiting time for writing
// to channel of clent, prevent from blocking.
func NewServer(bufsize int, timeout time.Duration) *Server {
	s := new(Server)
	s.functions = make(map[interface{}]interface{})
	s.ChanCall = make(chan *CallInfo, bufsize)
	s.timeout = timeout
	return s
}

// Register register handler for id.
func (s *Server) Register(id interface{}, f func([]interface{}) (ret interface{}, err error)) {
	if _, ok := s.functions[id]; ok {
		panic(fmt.Sprintf("function id %v: already registered", id))
	}
	s.functions[id] = f
}

// ret write result of ci to channel provided by ci.
func (s *Server) ret(ci *CallInfo, ri *RetInfo) (err error) {
	if ci.chanRet == nil {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			err = r.(error)
		}
	}()

	ri.cb = ci.cb

	select {
	case ci.chanRet <- ri:
	case <-time.After(time.Millisecond * s.timeout):
		s.SkipCounter++
	}
	return
}

// Exec execute call request from channel.
func (s *Server) Exec(ci *CallInfo) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if conf.LenStackBuf > 0 {
				buf := make([]byte, conf.LenStackBuf)
				l := runtime.Stack(buf, false)
				err = fmt.Errorf("%v: %s", r, buf[:l])
			} else {
				err = fmt.Errorf("%v", r)
			}
			log.Debug("%v", r)
			s.ret(ci, &RetInfo{err: fmt.Errorf("%v", r)})
		}
	}()

	f := s.functions[ci.id]
	if f == nil {
		panic(fmt.Sprintf("no function for %s", ci.id))
	}

	ret, err := f.(func([]interface{}) (ret interface{}, err error))(ci.args)
	return s.ret(ci, &RetInfo{ret: ret, err: err})
}

// Close do shutdown goroutine if has called s.Start() before, and close the call channel and return closed-msg to
// pending call requested before close.
func (s *Server) Close() {
	close(s.ChanCall)
	for ci := range s.ChanCall {
		err := s.Exec(ci)
		if err != nil {
			log.Error("%v", err)
		}
	}
}

// start client

// NewClient create a client, but not specify which server to attach, provide bufsize define the async buffer size of
// aysnc callback channel, and timeout define max wait time when send async call request.
func NewClient(size int, timeout time.Duration) *Client {
	c := new(Client)
	c.chanSyncRet = make(chan *RetInfo, 1)
	if size > 0 {
		c.ChanAsynRet = make(chan *RetInfo, size)
	}
	c.timeout = timeout
	return c
}

// call send the request call ci to server channel, if block is true, the call is sync call, or is a async call.
// If it is a sync call, it allow timeout if server is too busy.
func (c *Client) call(s *Server, ci *CallInfo, block bool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = r.(error)
		}
	}()

	// todo: should I first test if the server support the ci support the request.
	// should I wrapper getSupportRequest().

	if block {
		s.ChanCall <- ci
	} else {
		select {
		case s.ChanCall <- ci:
		case <-time.After(c.timeout):
			c.SkipCounter++
			c.pendingAsynCall--
			err = errors.New("server timeout")
		}
	}
	return
}

// Call do a sync call to attatched server.
func (c *Client) SynCall(s *Server, id interface{}, args ...interface{}) (interface{}, error) {
	_, err := validate(s, id)
	if err != nil {
		return nil, err
	}

	err = c.call(s, &CallInfo{
		id:      id,
		args:    args,
		chanRet: c.chanSyncRet,
	}, true)

	if err != nil {
		return nil, err
	}

	ri := <-c.chanSyncRet
	return ri.ret, ri.err
}

// AsyncCall do a async call to server.
func (c *Client) AsynCall(s *Server, id interface{}, _args ...interface{}) error {
	if len(_args) < 1 {
		panic("callback function not found")
	}

	args := _args[:len(_args)-1]
	cb := _args[len(_args)-1]

	switch cb.(type) {
	case func(ret interface{}, err error):
	default:
		//panic("definition of callback function is invalid")
		args = _args
		cb = nil
	}

	// !!double_check!!
	var _cb func(interface{}, error)
	if cb != nil {
		_cb = cb.(func(interface{}, error))
	}

	c.pendingAsynCall++

	if c.AllowOverFlood == false && c.pendingAsynCall >= cap(c.ChanAsynRet) {
		c.Cb(&RetInfo{err: errors.New("too many calls"), cb: _cb})
		return nil
	}

	//return c.asynCall(s, id, args, _cb)

	_, err := validate(s, id)
	if err != nil {
		c.ChanAsynRet <- &RetInfo{err: err, cb: _cb}
		return fmt.Errorf("no matching function for asynCall call: %v", id)
	}

	err = c.call(s, &CallInfo{
		id:      id,
		args:    args,
		chanRet: c.ChanAsynRet,
		cb:      _cb,
	}, false)

	if err != nil {
		c.ChanAsynRet <- &RetInfo{err: err, cb: _cb}
		return err
	}

	return nil
}

// exeCb do exec the callback of ri. It is a private method, only called by client internal.
// func execCb(ri *RetInfo) {

// }

// Cb do exec the callback of ri when the async call is finish handled by server.
func (c *Client) Cb(ri *RetInfo) {
	c.pendingAsynCall--

	//execCb(ri)
	defer func() {
		if r := recover(); r != nil {
			if conf.LenStackBuf > 0 {
				buf := make([]byte, conf.LenStackBuf)
				l := runtime.Stack(buf, false)
				log.Error("%v: %s", r, buf[:l])
			} else {
				log.Error("%v", r)
			}
		}
	}()

	if ri.cb != nil {
		ri.cb(ri.ret, ri.err)
	}
	return
}

// Close close goroutine internal if it exist, and do other cleanup job.
func (c *Client) Close() {
	//The rule of thumb here is that only writers should close channels.
}

// Helper function.

// SynCall do a sync call request to server.
func SynCall(s *Server, id interface{}, args ...interface{}) (interface{}, error) {
	// !!double_check!!
	c := NewClient(0, 1*time.Second)
	return c.SynCall(s, id, args...)
}

// validate validate the call id can map to handler of the server.
func validate(s *Server, id interface{}) (f interface{}, err error) {
	f = s.functions[id]
	if f == nil {
		err = fmt.Errorf("function id %v: function not registered", id)
		return
	}

	return
}
