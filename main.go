package main
import (
    "os"
    "io"
    "./importpsannot"
    )
func main() {
    if len(os.Args) < 2 {
       io.WriteString(os.Stderr, "usage: " + os.Args[0] + " <json_annotation_object>\n")
       os.Exit(1)
    }
    annotationString := os.Args[1]
    err := importpsannot.ProcessAnnotations(annotationString, os.Stdin, os.Stdout)
    if err != nil {
        io.WriteString(os.Stderr, "Error during processing:" + err.Error() + "\n")
    }
}
