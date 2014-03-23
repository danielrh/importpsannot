package main
import (
    "os"
    "io"
    )
func main() {
    if len(os.Args) < 2 {
       io.WriteString(os.Stderr, "usage: " + os.Args[0] + " <json_annotation_object>\n")
       os.Exit(1)
    }
    annotationString := os.Args[1]
    processAnnotations(annotationString, os.Stdin, os.Stdout)
}