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

package importpsannot
import (
    "errors"
    "strconv"
    "regexp"
    "io"
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
    Bookmarks []Annotation `json:"bookmarks"`
}
const CIRCULAR_BUFFER_SIZE = 4096 * 1024
const BUFFER_SEARCH_OVERLAP = 32

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

type Transform2D struct {
    AddX float64 // first add
    AddY float64
    Scale float64 // then scale
    Rotate90 bool
    MediaBox []float64
}

func (transform Transform2D) transform2d(rect []float64) {
    rectLen := len(rect)
    for i := 0; i + 1 < rectLen; i += 2 {
        x := rect[i]
        y := rect[i + 1]
        if transform.Rotate90 {
            cx := 0.5 * (transform.MediaBox[0] + transform.MediaBox[2])
            cy := 0.5 * (transform.MediaBox[1] + transform.MediaBox[3])
            x -= cx
            y -= cy
            tmp := x
            x = -y
            y = tmp
            x += cy
            y += cx
        }
        x = (x + transform.AddX) * transform.Scale
        y = (y + transform.AddY) * transform.Scale
        rect[i] = x
        rect[i + 1] = y
    }
    if transform.Rotate90 {
        tmp := rect[0]
        rect[0] = rect[2]
        rect[2] = tmp

    }
}

func outputLink(link Annotation,
                transform Transform2D,
                isUrl bool,
                output io.Writer) error {
    // we only support urls atm
    if isUrl {
        transform.transform2d(link.Rect[:])
        io.WriteString(output, "[ ")
        io.WriteString(output, link.Data)
        io.WriteString(output, " /Rect [")
        for _, bound := range(link.Rect) {
            io.WriteString(output, " ")
            io.WriteString(output, strconv.FormatFloat(bound, 'f', 4, 64))
        }
        io.WriteString(output, " ] /Subtype /Link /ANN pdfmark\n")
    }
    return nil
}

func getTransform(parserState ParserState, mediaBox []float64) (retval Transform2D){
    // we want to transform things from the mediabox to things that would fit into the parser state
    retval.MediaBox = mediaBox    
    mediaBoxWidth := mediaBox[2] - mediaBox[0]
    mediaBoxHeight := mediaBox[3] - mediaBox[1]
    if ( ((mediaBoxWidth / mediaBoxHeight) > 1) != (parserState.PageSizeX/parserState.PageSizeY > 1)) {
        scaleY := parserState.PageSizeY / mediaBoxWidth
        retval.Rotate90 = true
        mediaBoxMidpointX := (mediaBox[1] + mediaBox[3]) * scaleY * 0.5
        parserStateMidpointX := parserState.PageSizeX * 0.5
        retval.Scale = scaleY
        retval.AddX = (parserStateMidpointX - mediaBoxMidpointX) / scaleY
        retval.AddY = 0
    } else {
        scaleX := parserState.PageSizeX / mediaBoxWidth
        mediaBoxMidpointY := (mediaBox[1] + mediaBox[3]) * scaleX * 0.5
        parserStateMidpointY := parserState.PageSizeY * 0.5
        retval.Scale = scaleX
        retval.AddX = 0
        retval.AddY = (parserStateMidpointY - mediaBoxMidpointY) / scaleX
    }
    return
}

func outputPageLinks(annotations *map[string]Page,
                 output io.Writer,
                 parserState ParserState) error {
   page, ok := (*annotations)[strconv.FormatInt(int64(parserState.PageNumber), 10)]
   if ok {
       io.WriteString(output, "\ngsave\ninitmatrix\n")
       transform := getTransform(parserState, page.MediaBox)
       log.Printf("Page %v vs [%f %f] %v\n", page.MediaBox, parserState.PageSizeX, parserState.PageSizeY, transform)
       var err error
       for _, annotation := range(page.Urls) {
           err = outputLink(annotation, transform, true, output)
       }
       for _, annotation := range(page.Bookmarks) {
           err = outputLink(annotation, transform, false, output)
       }
       io.WriteString(output, "grestore")
       return err
   }
   return nil
}
var MEDIABOX_REGEX = regexp.MustCompile(
        `\s*\[\s*([-+]?[0-9]*\.?[0-9]+([eE][-+]?[0-9]+)?)` +
        `\s+([-+]?[0-9]*\.?[0-9]+([eE][-+]?[0-9]+)?)\s*\]`)

func parsePageSize(buffer[] byte) (x,y float64, err error) {
    matches := MEDIABOX_REGEX.FindSubmatchIndex(buffer)
    if matches == nil {
       err = errors.New("No space for page size")
    } else {
        x, err = strconv.ParseFloat(string(buffer[matches[2] : matches[3]]), 64)
        y, err = strconv.ParseFloat(string(buffer[matches[6] : matches[7]]), 64)
    }
    return
}

func processPage(annotations *map[string]Page,
                 buffer []byte,
                 output io.Writer,
                 parserState ParserState) (ParserState, error) {
    sizeFlushed := 0
    var maxTokenSize = 10
    searchLimit := len(buffer) - maxTokenSize
    if searchLimit > CIRCULAR_BUFFER_SIZE {
        searchLimit = CIRCULAR_BUFFER_SIZE
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
        if page && b5 == 'S' && b6 == 'i' && b7 == 'z' && b8 == 'e' {
            x, y, perr := parsePageSize(buffer[i + 9 :])
            if perr == nil {
                parserState.PageSizeX = x
                parserState.PageSizeY = y
            }
            //check limit for parsing the page size and then parse page size
        }
        show := b1 == 's' && b2 == 'h' && b3 == 'o' && b4 == 'w';
        if show && b5 == 'p' && b6 == 'a' && b7 == 'g' && b8 == 'e' && isBlank(b0) && isBlank(b9) {
            wrote, err := output.Write(buffer[sizeFlushed : i])
            if err != nil || wrote < i - sizeFlushed {
               if err == nil {
                   err = errors.New("Wrote less than full number of bytes")
               }
               return parserState, err
            }
            sizeFlushed = i
            outputPageLinks(annotations, output, parserState)
            parserState.PageNumber += 1
        }
    }
    if len(buffer) < CIRCULAR_BUFFER_SIZE {
        _, err := output.Write(buffer[sizeFlushed:])
        if err != nil {
            return parserState, err
        }
    } else {
        _, err := output.Write(buffer[sizeFlushed:CIRCULAR_BUFFER_SIZE])
        if err != nil {
            return parserState, err
        }
    }
    return parserState, nil
}

// This function inserts an annotation structure into the input stream and writes it to the output
// Both input and output streams contain postscript (.ps) data
func ProcessAnnotations(annotationStringJson string, input io.Reader, output io.Writer) error {
    parserState := ParserState{612.0, 792.0, false, 0, 0, }
    var annotations map[string]Page
    err := json.Unmarshal([]byte(annotationStringJson), &annotations);
    if err != nil {
        log.Fatalf("%v\n", err)
    }
    var buffer [CIRCULAR_BUFFER_SIZE + BUFFER_SEARCH_OVERLAP]byte
    readStartOffset := 0
    for err == nil || readStartOffset > 0 {
        copy(buffer[0 : readStartOffset],
             buffer[CIRCULAR_BUFFER_SIZE : CIRCULAR_BUFFER_SIZE + readStartOffset])
        size := readStartOffset
        for err == nil {
            slice := buffer[size : ]
            if len(slice) == 0 {
                break
            }
            curRead := 0
            curRead, err = input.Read(slice)
            size += curRead
        }
        {
            var writeError error
            parserState, writeError = processPage(&annotations,
                buffer[0 : size],
                output,
                parserState)
            if writeError != nil {
                err = writeError
            }
        }
        readStartOffset = size - CIRCULAR_BUFFER_SIZE
    }
    if err == io.EOF {
        return nil
    }
    return err
}

