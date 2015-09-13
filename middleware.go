/*
Copyright (c) 2015 Jeevanandam M (jeeva@myjeeva.com), All rights reserved.

resty source code and usage is governed by a MIT style
license that can be found in the LICENSE file.
*/
package resty

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

//
// Request Middleware(s)
//

func parseRequestUrl(c *Client, r *Request) error {
	// Parsing request URL
	reqUrl, err := url.Parse(r.Url)
	if err != nil {
		return err
	}

	// If Request.Url is relative path then added c.HostUrl into
	// the request URL otherwise Request.Url will be used as-is
	if !reqUrl.IsAbs() {
		if !strings.HasPrefix(r.Url, "/") {
			r.Url = "/" + r.Url
		}

		reqUrl, err = url.Parse(c.HostUrl + r.Url)
		if err != nil {
			return err
		}
	}

	// Adding Query Param
	query := reqUrl.Query()
	for k, v := range c.QueryParam {
		for _, pv := range v {
			query.Add(k, pv)
		}
	}
	for k, v := range r.QueryParam {
		for _, pv := range v {
			query.Add(k, pv)
		}
	}

	reqUrl.RawQuery = query.Encode()
	r.Url = reqUrl.String()

	return nil
}

func parseRequestHeader(c *Client, r *Request) error {
	hdr := http.Header{}
	for k := range c.Header {
		hdr.Set(k, c.Header.Get(k))
	}
	for k := range r.Header {
		hdr.Set(k, r.Header.Get(k))
	}

	if IsStringEmpty(hdr.Get(hdrUserAgentKey)) {
		hdr.Set(hdrUserAgentKey, fmt.Sprintf(hdrUserAgentValue, Version))
	} else {
		hdr.Set("X-"+hdrUserAgentKey, fmt.Sprintf(hdrUserAgentValue, Version))
	}

	if IsStringEmpty(hdr.Get(hdrAcceptKey)) && !IsStringEmpty(hdr.Get(hdrContentTypeKey)) {
		hdr.Set(hdrAcceptKey, hdr.Get(hdrContentTypeKey))
	}

	r.Header = hdr

	return nil
}

func parseRequestBody(c *Client, r *Request) (err error) {
	if r.Method == POST || r.Method == PUT || r.Method == PATCH {
		// Handling Multipart
		if r.isMultiPart && !(r.Method == PATCH) {
			r.bodyBuf = &bytes.Buffer{}
			w := multipart.NewWriter(r.bodyBuf)

			for p := range c.FormData {
				w.WriteField(p, c.FormData.Get(p))
			}

			for p := range r.FormData {
				if strings.HasPrefix(p, "@") { // file
					err = addFile(w, p[1:], r.FormData.Get(p))
					if err != nil {
						return
					}
				} else { // form value
					w.WriteField(p, r.FormData.Get(p))
				}
			}

			r.Header.Set(hdrContentTypeKey, w.FormDataContentType())
			err = w.Close()

			goto CL
		}

		// Handling Form Data
		if len(c.FormData) > 0 || len(r.FormData) > 0 {
			formData := url.Values{}

			for p := range c.FormData {
				formData.Set(p, c.FormData.Get(p))
			}

			for p := range r.FormData { // takes precedence
				formData.Set(p, r.FormData.Get(p))
			}

			r.bodyBuf = bytes.NewBuffer([]byte(formData.Encode()))
			r.Header.Set(hdrContentTypeKey, formContentType)
			r.isFormData = true

			goto CL
		}

		// Handling Request body
		if r.Body != nil {
			contentType := r.Header.Get(hdrContentTypeKey)
			if IsStringEmpty(contentType) {
				contentType = DetectContentType(r.Body)
				r.Header.Set(hdrContentTypeKey, contentType)
			}

			var bodyBytes []byte
			isMarshal := IsMarshalRequired(r.Body)
			if IsJsonType(contentType) && isMarshal {
				bodyBytes, err = json.Marshal(&r.Body)
			} else if IsXmlType(contentType) && isMarshal {
				bodyBytes, err = xml.Marshal(&r.Body)
			} else if b, ok := r.Body.(string); ok {
				bodyBytes = []byte(b)
			} else if b, ok := r.Body.([]byte); ok {
				bodyBytes = b
			}

			if err != nil {
				return
			}

			// []byte into Buffer
			if bodyBytes == nil {
				err = errors.New("Unsupported 'Body' type/value")
				return
			} else {
				r.bodyBuf = bytes.NewBuffer(bodyBytes)
			}
		}
	}

CL:
	if c.setContentLength || r.setContentLength { // by default resty won't set content length
		r.Header.Set(hdrContentLengthKey, fmt.Sprintf("%d", r.bodyBuf.Len()))
	}

	return
}

func createHttpRequest(c *Client, r *Request) (err error) {
	if r.bodyBuf == nil {
		r.RawRequest, err = http.NewRequest(r.Method, r.Url, nil)
	} else {
		r.RawRequest, err = http.NewRequest(r.Method, r.Url, r.bodyBuf)
	}

	// Add headers into http request
	r.RawRequest.Header = r.Header

	// Add cookies into http request
	for _, cookie := range c.Cookies {
		r.RawRequest.AddCookie(cookie)
	}

	return
}

func addCredentials(c *Client, r *Request) error {
	var isBasicAuth bool
	// Basic Auth
	if r.UserInfo != nil { // takes precedence
		r.RawRequest.SetBasicAuth(r.UserInfo.Username, r.UserInfo.Password)
		isBasicAuth = true
	} else if c.UserInfo != nil {
		r.RawRequest.SetBasicAuth(c.UserInfo.Username, c.UserInfo.Password)
		isBasicAuth = true
	}
	if isBasicAuth && strings.HasPrefix(r.Url, "http") {
		c.Log.Println("WARNING - Using Basic Auth in HTTP mode is not secure.")
	}

	// Token Auth
	if !IsStringEmpty(r.Token) { // takes precedence
		r.RawRequest.Header.Set(hdrAuthorizationKey, "Bearer "+r.Token)
	} else if !IsStringEmpty(c.Token) {
		r.RawRequest.Header.Set(hdrAuthorizationKey, "Bearer "+c.Token)
	}

	return nil
}

func requestLogger(c *Client, r *Request) error {
	if c.Debug {
		rr := r.RawRequest
		c.Log.Println("")
		c.disableLogPrefix()
		c.Log.Println("---------------------- REQUEST LOG -----------------------")
		c.Log.Printf("%s  %s  %s\n", r.Method, rr.URL.RequestURI(), rr.Proto)
		c.Log.Printf("HOST   : %s", rr.URL.Host)
		c.Log.Println("HEADERS:")
		for h, v := range rr.Header {
			c.Log.Printf("%25s: %v", h, strings.Join(v, ", "))
		}
		c.Log.Printf("BODY   :\n%v", getRequestBodyString(r))
		c.Log.Println("----------------------------------------------------------")
		c.enableLogPrefix()
	}

	return nil
}

//
// Response Middleware(s)
//

func responseLogger(c *Client, res *Response) error {
	if c.Debug {
		c.Log.Println("")
		c.disableLogPrefix()
		c.Log.Println("---------------------- RESPONSE LOG -----------------------")
		c.Log.Printf("STATUS : %s", res.Status())
		c.Log.Printf("TIME   : %v", res.Time())
		c.Log.Println("HEADERS:")
		for h, v := range res.Header() {
			c.Log.Printf("%30s: %v", h, strings.Join(v, ", "))
		}
		c.Log.Printf("BODY   :\n%v", getResponseBodyString(res))
		c.Log.Println("----------------------------------------------------------")
		c.enableLogPrefix()
	}

	return nil
}

func parseResponseBody(c *Client, res *Response) (err error) {
	// Handles only JSON or XML content type
	ct := res.Header().Get(hdrContentTypeKey)
	if IsJsonType(ct) || IsXmlType(ct) {
		// Considered as Result
		if res.StatusCode() > 199 && res.StatusCode() < 300 {
			if res.Request.Result != nil {
				err = Unmarshal(ct, res.Body, res.Request.Result)
			}
		}

		// Considered as Error
		if res.StatusCode() > 399 {
			// global error interface
			if res.Request.Error == nil && c.Error != nil {
				res.Request.Error = c.Error
			}

			if res.Request.Error != nil {
				err = Unmarshal(ct, res.Body, res.Request.Error)
			}
		}
	}

	return
}
