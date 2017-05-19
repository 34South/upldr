package main

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"

	"encoding/json"
	"github.com/34South/envr"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awsutil"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"strings"
)

type response struct {
	Code    int    `json:"code"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type uploadObject struct {
	Bucket string `json:"bucket"`
	Path   string `json:"path"`
	Name   string `json:"name"`
}

var tpl *template.Template

func init() {

	envr.New("myEnv", []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_REGION",
		"AWS_BUCKET",
		"URL_SUCCESS",
		"URL_FAIL",
	}).Passive().Fatal()

	tpl = template.Must(template.ParseGlob("templates/*"))
}

func main() {

	http.Handle("/css/", http.StripPrefix("/css", http.FileServer(http.Dir("./public/css"))))
	http.Handle("/js/", http.StripPrefix("/js", http.FileServer(http.Dir("./public/js"))))
	http.Handle("/img/", http.StripPrefix("/img", http.FileServer(http.Dir("./public/img"))))
	http.Handle("/favicon.ico", http.NotFoundHandler())

	// Just testing pages...
	http.HandleFunc("/", index)
	http.HandleFunc("/upload", upload)
	http.HandleFunc("/success", success)
	http.HandleFunc("/fail", fail)

	// The actual handler
	http.HandleFunc("/upldr", uploadHandler)

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func index(w http.ResponseWriter, r *http.Request) {

	tpl.ExecuteTemplate(w, "index", nil)
}

func upload(w http.ResponseWriter, r *http.Request) {

	tpl.ExecuteTemplate(w, "upload", nil)
}

func success(w http.ResponseWriter, r *http.Request) {

	code := r.URL.Query().Get("code")
	status := r.URL.Query().Get("status")
	msg := r.URL.Query().Get("msg")
	message := "The request succeeded - code %s, status %s, message: %s"
	message = fmt.Sprintf(message, code, status, msg)

	tpl.ExecuteTemplate(w, "success", message)
}

func fail(w http.ResponseWriter, r *http.Request) {

	code := r.URL.Query().Get("code")
	status := r.URL.Query().Get("status")
	msg := r.URL.Query().Get("msg")
	message := "The request failed - code %s, status %s, message: %s"
	message = fmt.Sprintf(message, code, status, msg)

	tpl.ExecuteTemplate(w, "fail", message)
}

// uploadHandler handles the actual POST upload
func uploadHandler(w http.ResponseWriter, r *http.Request) {

	rb := response{}

	// Maybe we don't need to respond with JSON ever, as this is to be used in HTML.
	// Really, we just need to callback URLs. Leave this one for now but most likely they can
	// all go, or be moved to a JSON response endpoint.
	if r.Method != "POST" {
		rb.Code = http.StatusMethodNotAllowed
		rb.Status = http.StatusText(rb.Code)
		rb.Message = "Must be a POST request"
		url := fmt.Sprintf("%v?code=%v&status=%v&msg=%v",
			os.Getenv("URL_FAIL"), rb.Code, rb.Status, rb.Message)
		// Can't do a 400 because it won't redirect
		fmt.Println(url)
		http.Redirect(w, r, url, http.StatusSeeOther)
		//respond(w, rb)
		return
	}

	// Parse form? Is this required for large files?
	r.ParseMultipartForm(32 << 20)

	// Parse the file first to get the default file name
	f, fh, err := r.FormFile("upldr-file")
	if err != nil {
		rb.Code = http.StatusBadRequest
		rb.Status = http.StatusText(rb.Code)
		rb.Message = ".formFile() err - " + err.Error() + ". Maybe use some js to make sure a file is selected."
		url := fmt.Sprintf("%v?code=%v&status=%v&msg=%v",
			os.Getenv("URL_FAIL"), rb.Code, rb.Status, rb.Message)
		// Can't do a 400 because it won't redirect
		http.Redirect(w, r, url, http.StatusSeeOther)
		return
	}
	defer f.Close()

	// Get object vars sorted, default values first...
	uo := uploadObject{
		Bucket: os.Getenv("AWS_BUCKET"),
		Path:   "/",
		Name:   fh.Filename,
	}

	// Override defaults if requested...
	if len(r.FormValue("upldr-bucket")) > 0 {
		uo.Bucket = r.FormValue("upldr-bucket")
	}
	if len(r.FormValue("upldr-path")) > 0 {
		uo.Path = r.FormValue("upldr-path")
	}
	// Make sure path has a trailing slash!
	if !strings.HasSuffix(uo.Path, "/") {
		uo.Path = uo.Path + "/"
	}
	if len(r.FormValue("upldr-name")) > 0 {
		uo.Name = r.FormValue("upldr-name")
	}
	fmt.Println(uo)

	// Get a pointer to the file posted in...
	inFile, err := fh.Open()
	if err != nil {
		rb.Code = http.StatusBadRequest
		rb.Status = http.StatusText(rb.Code)
		rb.Message = fmt.Sprintf(".Open() err %s", err.Error())
		url := fmt.Sprintf("%v?code=%v&status=%v&msg=%v",
			os.Getenv("URL_FAIL"), rb.Code, rb.Status, rb.Message)
		// Can't do a 400 because it won't redirect
		http.Redirect(w, r, url, http.StatusSeeOther)
		return
	}
	defer inFile.Close()
	//xb, err := ioutil.ReadAll(inFile)
	//fmt.Println(string(xb))

	// Copy inFile (multipart.File) to get a *File before upload...
	outFile, err := os.OpenFile("./tmp/"+uo.Name, os.O_WRONLY|os.O_CREATE, 0666)
	written, err := io.Copy(outFile, inFile)
	if err != nil {
		rb.Code = http.StatusInternalServerError
		rb.Status = http.StatusText(rb.Code)
		rb.Message = fmt.Sprintf("io.Copy() err %s", err.Error())
		url := fmt.Sprintf("%v?code=%v&status=%v&msg=%v",
			os.Getenv("URL_FAIL"), rb.Code, rb.Status, rb.Message)
		// Can't do a 400 because it won't redirect
		http.Redirect(w, r, url, http.StatusSeeOther)
		return
	}
	defer outFile.Close()
	fmt.Println("uploaded file '", fh.Filename, "' saved as '", uo.Name+"' - length:", strconv.Itoa(int(written)))

	// Now copy the local file to S3...
	awsAccessKeyId := os.Getenv("AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	token := ""
	awsRegion := os.Getenv("AWS_REGION")
	awsBucket := uo.Bucket
	creds := credentials.NewStaticCredentials(awsAccessKeyId, awsSecretAccessKey, token)
	_, err = creds.Get()
	if err != nil {
		rb.Code = http.StatusInternalServerError
		rb.Status = http.StatusText(rb.Code)
		rb.Message = fmt.Sprintf("bad credentials: %s", err.Error())
		url := fmt.Sprintf("%v?code=%v&status=%v&msg=%v",
			os.Getenv("URL_FAIL"), rb.Code, rb.Status, rb.Message)
		// Can't do a 400 because it won't redirect
		http.Redirect(w, r, url, http.StatusSeeOther)
		return
	}
	cfg := aws.NewConfig().WithRegion(awsRegion).WithCredentials(creds)

	sess := session.Must(session.NewSession())
	svc := s3.New(sess, cfg)

	file, err := os.Open(outFile.Name())
	if err != nil {
		rb.Code = http.StatusInternalServerError
		rb.Status = http.StatusText(rb.Code)
		rb.Message = fmt.Sprintf("err opening file: %s", err.Error())
		url := fmt.Sprintf("%v?code=%v&status=%v&msg=%v",
			os.Getenv("URL_FAIL"), rb.Code, rb.Status, rb.Message)
		// Can't do a 400 because it won't redirect
		http.Redirect(w, r, url, http.StatusSeeOther)
		return
	}
	defer file.Close()

	fileInfo, _ := file.Stat()
	size := fileInfo.Size()
	buffer := make([]byte, size) // read file content to buffer
	file.Read(buffer)
	fileBytes := bytes.NewReader(buffer)
	fileType := http.DetectContentType(buffer)
	path := uo.Path + uo.Name
	params := &s3.PutObjectInput{
		Bucket:        aws.String(awsBucket),
		Key:           aws.String(path),
		Body:          fileBytes,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(fileType),
	}
	resp, err := svc.PutObject(params)
	if err != nil {
		rb.Code = http.StatusInternalServerError
		rb.Status = http.StatusText(rb.Code)
		rb.Message = fmt.Sprintf("bad response: %s", err.Error())
		url := fmt.Sprintf("%v?code=%v&status=%v&msg=%v",
			os.Getenv("URL_FAIL"), rb.Code, rb.Status, rb.Message)
		// Can't do a 400 because it won't redirect
		http.Redirect(w, r, url, http.StatusSeeOther)
		return
	}

	fmt.Printf("response %s\n", awsutil.StringValue(resp))

	fmt.Printf("Cleaning up local file... ")
	err = os.RemoveAll(file.Name())
	if err != nil {
		fmt.Println("os.RemoveAll() could not remove file -", err)
		return
	}
	fmt.Println("done")


	rb.Code = http.StatusOK
	rb.Status = http.StatusText(rb.Code)
	rb.Message = "Uploaded to " + uo.Bucket + ":" + uo.Path + uo.Name
	fmt.Println(rb)
	url := fmt.Sprintf("%v?code=%v&status=%v&msg=%v",
		os.Getenv("URL_SUCCESS"), rb.Code, rb.Status, rb.Message)
	// Can't do a 400 because it won't redirect
	http.Redirect(w, r, url, http.StatusSeeOther)
	return
}

// respond does JSON responses
func respond(w http.ResponseWriter, rb response) {

	xb, err := json.Marshal(rb)
	if err != nil {
		fmt.Println("respond() could not marshal json -", err)
		return
	}

	j := string(xb)
	fmt.Println(j)
	w.Header().Add("Content-type", "application/json")
	w.WriteHeader(rb.Code)
	io.WriteString(w, j)
}
