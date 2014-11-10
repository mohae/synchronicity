// Copyright 2014 Joel Scoble (github.com/mohae) All rights reserved.
// Use of this source code is governed by a BSD-style license that
// can be found in the LICENSE file.
//
// Based on Richard Clayton's blog post on pipelines:
//   https://rclayton.silvrback.com/pipelines-in-golang
// Which is based on Sameer Ajmani's pipeline post:
//   https://blog.golang.org/pipelines
package synchronicity

type Pipe interface {
	Process(in chan *FileData) chan *FileData
}

func NewPipeline(pipes ...Pipe) Pipeline {
	head := make(chan *FileData)
	var next_chan chan *FileData
	for _, pipe := range pipes {
		if next_chan == nil {
			next_chan = pipe.Process(head)
		} else {
			next_chan = pipe.Process(next_chan)
		}
	}
	return Pipeline{head: head, tail: next_chan}
}

type Pipeline struct {
	head chan *FileData
	tail chan *FileData
}

func (p *Pipeline) Enqueue(item *FileData) {
	p.head <- item
}

func (p *Pipeline) Dequeue(handler func(*FileData)) {
	for i := range p.tail {
		handler(i)
	}
}

func (p *Pipeline) Close() {
	close(p.head)
}
