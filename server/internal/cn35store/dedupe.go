package cn35store

// dedupe.go ITEM350-11/12：写面并发去重（in-flight singleflight）。
//
// 背景：客户端兑换上行只有 id/count（ExchangeImageButton.cs 构串），
// JSONRequester 不做传输层重试——同一次业务提交没有服务端可见的稳定
// 请求标识，持久幂等只能靠可选 request_key 台账。对「双击/并发抢在
// TouchDisable 生效前抵达」这一真实重复通道，本文件提供按
// (user,operation,载荷) 键的在途去重：后到者等待首个执行完成并复用
// 其结果，首个完成后键即释放——用户明确再次购买不受任何时间窗吞并。
// 跨进程的协议层严格防重仍属协议阻断，见 plans/cn35-weekly-resource-flow。

// inflightCall 一个在途写操作；Waiter 读 Res/Err 复用结果。
type inflightCall struct {
	done chan struct{}
	res  any
	err  error
}

// dedupe 以 key 串行判重：首个调用执行 fn；并发期间同键调用阻塞等待，
// 返回首个调用结果的副本并标 dup=true。fn 结束后键释放，后续同键调用
// 正常执行（不再视为重复）。
func (s *Store) dedupe(key string, fn func() (any, error)) (res any, dup bool, err error) {
	s.inflightMu.Lock()
	if s.inflight == nil {
		s.inflight = map[string]*inflightCall{}
	}
	if c, ok := s.inflight[key]; ok {
		s.inflightMu.Unlock()
		<-c.done
		return c.res, true, c.err
	}
	c := &inflightCall{done: make(chan struct{})}
	s.inflight[key] = c
	s.inflightMu.Unlock()

	res, err = fn()

	s.inflightMu.Lock()
	c.res, c.err = res, err
	delete(s.inflight, key)
	close(c.done)
	s.inflightMu.Unlock()
	return res, false, err
}
