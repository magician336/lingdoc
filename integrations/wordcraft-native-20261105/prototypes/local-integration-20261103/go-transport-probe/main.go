package main

import (
 "bytes"
 "context"
 "crypto/tls"
 "encoding/json"
 "fmt"
 "net"
 "net/http"
 "net/http/httptrace"
 "os"
 "path/filepath"
 "strings"
 "time"
 secutils "github.com/Tencent/WeKnora/internal/utils"
)

func main() {
 var cfg struct { Model string `json:"model"`; Key string `json:"api_key"` }
 data,err:=os.ReadFile("../../../candidates/lingdoc-native-workspace-20261027/runtime/ai-private/local-ai.json")
 if err!=nil || json.Unmarshal(data,&cfg)!=nil || cfg.Key=="" {fmt.Println(`{"error":"configuration_unavailable"}`);os.Exit(1)}
 chat:=len(os.Args)>1 && os.Args[1]=="--chat"
 modes:=[]bool{false,true};if chat {modes=[]bool{true}}
 for _,safe:=range modes {
  label:="standard";if safe {label="existing_ssrf_transport"}
  result:=map[string]any{"transport":label,"endpoint_host":"api.deepseek.com","configured_model":cfg.Model}
  started:=time.Now()
  timeout:=25*time.Second;if chat {timeout=60*time.Second}
  ctx,cancel:=context.WithTimeout(context.Background(),timeout)
  trace:=&httptrace.ClientTrace{DNSDone:func(info httptrace.DNSDoneInfo){result["dns_ok"]=info.Err==nil;result["dns_answers"]=len(info.Addrs)},TLSHandshakeDone:func(_ tls.ConnectionState,e error){result["tls_ok"]=e==nil},GotFirstResponseByte:func(){result["first_byte_seconds"]=time.Since(started).Seconds()}}
  req,_:=http.NewRequestWithContext(httptrace.WithClientTrace(ctx,trace),"GET","https://api.deepseek.com/v1/models",nil)
  if chat {
   paths,_:=filepath.Glob("../artifacts/workflow-model/2026-11-03T10-15-12-510Z/runtime/logs/*/go.log")
   if len(paths)!=1 {panic("fixture log unavailable")}
   raw,_:=os.ReadFile(paths[0]);var payload []byte
   for _,line:=range strings.Split(string(raw),"\n") {if strings.HasPrefix(line,`{"max_tokens"`) && json.Valid([]byte(strings.TrimSpace(line))) {payload=[]byte(strings.TrimSpace(line));break}}
   if len(payload)==0 {panic("fixture request unavailable")}
   req,_=http.NewRequestWithContext(httptrace.WithClientTrace(ctx,trace),"POST","https://api.deepseek.com/v1/chat/completions",bytes.NewReader(payload));req.Header.Set("Content-Type","application/json");result["operation"]="same_fictional_payload_chat"
  }
  req.Header.Set("Authorization","Bearer "+cfg.Key)
  proxy,_:=http.ProxyFromEnvironment(req);result["proxy_configured"]=proxy!=nil
  transport:=&http.Transport{Proxy:http.ProxyFromEnvironment,TLSHandshakeTimeout:10*time.Second,IdleConnTimeout:90*time.Second,MaxIdleConnsPerHost:5}
  client:=&http.Client{Transport:transport}
  if safe {transport.DialContext=secutils.SSRFSafeDialContext;client=secutils.NewSSRFSafeHTTPClientWithTransport(secutils.SSRFSafeHTTPClientConfig{Timeout:0,MaxRedirects:10},transport)}
  resp,err:=client.Do(req)
  if err!=nil {
   result["error_type"]=fmt.Sprintf("%T",err)
   if e,ok:=err.(net.Error);ok {result["timeout"]=e.Timeout()}
   result["context_expired"]=ctx.Err()!=nil
  } else {
   result["http_status"]=resp.StatusCode
   if chat {
    var body struct {Choices []struct {Message struct{Content string `json:"content"`} `json:"message"`;Finish string `json:"finish_reason"`} `json:"choices"`;Error struct{Code any `json:"code"`;Type any `json:"type"`} `json:"error"`}
    if json.NewDecoder(resp.Body).Decode(&body)==nil {
     if resp.StatusCode==200 && len(body.Choices)>0 {result["finish_reason"]=body.Choices[0].Finish;var output struct{Reply string `json:"reply"`;Citations []any `json:"citations"`};result["valid_reply_json"]=json.Unmarshal([]byte(body.Choices[0].Message.Content),&output)==nil && output.Reply!="";result["reply_characters"]=len([]rune(output.Reply))} else {result["error_code"]=body.Error.Code;result["error_type"]=body.Error.Type}
    }
   } else {
    var body struct {Data []struct{ID string `json:"id"`} `json:"data"`}
    if resp.StatusCode==200 && json.NewDecoder(resp.Body).Decode(&body)==nil {exists:=false;for _,m:=range body.Data {if m.ID==cfg.Model {exists=true}};result["configured_model_exists"]=exists}
   }
   resp.Body.Close()
  }
  cancel();transport.CloseIdleConnections();result["elapsed_seconds"]=time.Since(started).Seconds();encoded,_:=json.Marshal(result);fmt.Println(string(encoded))
 }
}
