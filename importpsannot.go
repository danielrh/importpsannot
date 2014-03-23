/////////////////////////////////////////////////////////////////////////////////////////////////
// Copyright (c) 2014, Daniel Reiter Horn
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without modification, are permitted
// provided that the following conditions are met:
//
// 1. Redistributions of source code must retain the above copyright notice, this list of
//    conditions and the following disclaimer.
//
// 2. Redistributions in binary form must reproduce the above copyright notice, this list of
//    conditions and the following disclaimer in the documentation and/or other materials
//    provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR
// IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY
// AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR
// CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
// CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
// SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
// THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR
// OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
// POSSIBILITY OF SUCH DAMAGE.
//////////////////////////////////////////////////////////////////////////////////////////////////

package main
import (
    "bytes"
    "errors"
    "strconv"
    "io"
//    "io/ioutil"
    "log"
    "encoding/json"
    )

type Annotation struct {
    Uri string `json:"uri"`
    Data string `json:"data"`
    Rect []float64 `json:"rect"`
}

type Page struct {
    MediaBox []float64    `json:"mediabox"`
    Urls []Annotation `json:"urls"`
    Bookmarks []Annotation `json:"urls"`
}
const circular_buffer_size = 4096 * 1024
const buffer_search_overlap = 32

type ParserState struct {
   PageSizeX float64
   PageSizeY float64
   Comment bool
   QuoteLevel int
   PageNumber int
}

func isBlank(glyph byte) bool {
   return glyph == ' ' || glyph == '\n' || glyph == '\t'
}

func outputLink(mediaBox []float64,
                link Annotation,
                isUrl bool,
                output io.Writer,
                parserState ParserState) error {
    // we only support urls atm
    if isUrl {
       output.Write([]byte("\n"))
       output.Write([]byte(link.Data))
    }
    return nil
}

func outputPageLinks(annotations *map[string]Page,
                 output io.Writer,
                 parserState ParserState) (err error) {
   page, ok := (*annotations)[strconv.FormatInt(int64(parserState.PageNumber), 10)]
   if ok {
       log.Printf("Page %v vs [%f %f]\n", page.MediaBox, parserState.PageSizeX, parserState.PageSizeY)
       var err error
       for _, annotation := range(page.Urls) {
           err = outputLink(page.MediaBox, annotation, true, output, parserState)
       }
       for _, annotation := range(page.Bookmarks) {
           err = outputLink(page.MediaBox, annotation, false, output, parserState)
       }
       return err
   }
   return nil
}

func parsePageSize(buffer[] byte) (x,y float64, err error) {
    firstFloatEnd := bytes.IndexByte(buffer, ' ')
    secondFloatEnd := bytes.IndexByte(buffer, ']')
    if firstFloatEnd != -1 && secondFloatEnd != -1 {
        x, err = strconv.ParseFloat(string(buffer[0 : firstFloatEnd]), 64)
        if err == nil {
            y, err = strconv.ParseFloat(string(buffer[firstFloatEnd + 1: secondFloatEnd]), 64)
        }
    } else {
        err = errors.New("No space for page size")
    }
    return
}

func processPage(annotations *map[string]Page,
                 buffer []byte,
                 output io.Writer,
                 parserState ParserState) ParserState {
    size_flushed := 0
    var maxTokenSize = 10
    searchLimit := len(buffer) - maxTokenSize
    if searchLimit > circular_buffer_size {
        searchLimit = circular_buffer_size
    }
    for i := 0; i < searchLimit; i++ {
        b0 := buffer[i + 0]
        b1 := buffer[i + 1]
        b2 := buffer[i + 2]
        b3 := buffer[i + 3]
        b4 := buffer[i + 4]
        b5 := buffer[i + 5]
        b6 := buffer[i + 6]
        b7 := buffer[i + 7]
        b8 := buffer[i + 8]
        b9 := buffer[i + 9]
        if b0 == '\n' {
            parserState.QuoteLevel = 0
            parserState.Comment = false
        }
        if parserState.Comment {
            continue
        }
        if b0 == '(' {
            parserState.QuoteLevel += 1
        }
        if b0 != '\\' && b1 == ')' && parserState.QuoteLevel > 0 {
            parserState.QuoteLevel -= 1
        }
        if parserState.QuoteLevel > 0 {
            continue
        }
        if b0 == '%' && b1 == '%' {
            //comments are allowed within parens
            parserState.Comment = true
        }
        page := b0 == '/' && b1 == 'P' && b2 == 'a' && b3 == 'g' && b4 == 'e';
        if page && b5 == 'S' && b6 == 'i' && b7 == 'z' && b8 == 'e' && b9 =='[' {
            x, y, perr := parsePageSize(buffer[i + 10 :])
            if perr == nil {
                parserState.PageSizeX = x
                parserState.PageSizeY = y
            }
            //check limit for parsing the page size and then parse page size
        }
        show := b1 == 's' && b2 == 'h' && b3 == 'o' && b4 == 'w';
        if show && b5 == 'p' && b6 == 'a' && b7 == 'g' && b8 == 'e' && isBlank(b0) && isBlank(b9) {
            wrote, err := output.Write(buffer[size_flushed : i])
            if err != nil || wrote < i - size_flushed {
               panic(err)
            }
            size_flushed = i
            outputPageLinks(annotations, output, parserState)
            parserState.PageNumber += 1
        }
    }
    if len(buffer) < circular_buffer_size {
        _, err := output.Write(buffer[size_flushed:])
        if err != nil {
            panic(err)
        }
    } else {
        _, err := output.Write(buffer[size_flushed:circular_buffer_size])
        if err != nil {
            panic(err)
        }
    }
    return parserState
}

// This function inserts an annotation structure into the input stream and writes it to the output
// Both input and output streams contain postscript (.ps) data
func processAnnotations(annotationStringJson string, input io.Reader, output io.Writer) {
    parserState := ParserState{612.0, 792.0, false, 0, 0, }
    var annotations map[string]Page
    err := json.Unmarshal([]byte(annotationStringJson), &annotations);
    if err != nil {
        log.Fatalf("%v\n", err)
    }
    var buffer [circular_buffer_size + buffer_search_overlap]byte
    read_start_offset := 0
    for err == nil || read_start_offset > 0 {
        copy(buffer[0 : read_start_offset],
             buffer[circular_buffer_size : circular_buffer_size + read_start_offset])
        size := read_start_offset
        for err == nil {
            slice := buffer[size : ]
            if len(slice) == 0 {
                break
            }
            cur_read := 0
            cur_read, err = input.Read(slice)
            size += cur_read
        }
        parserState = processPage(&annotations,
            buffer[0 : size],
            output,
            parserState)
        read_start_offset = size - circular_buffer_size
    }
}
