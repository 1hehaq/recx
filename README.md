<div align="center">

  <h1><a href="https://github.com/1hehaq/recx">recx</a></h1>

  
> recx is a crawler for finding reflected parameters!

</div>

<hr>
<br>

## `features`

- fast and efficient crawling
- real time parameter reflection detection
- special character filtering detection
- smart context validation
- multiple domain support
- zero false positives
- pipe line friendly output

<br>
<br>

`installation`

```bash
go install github.com/1hehaq/recx
```

`or build from source`

```bash
git clone https://github.com/1hehaq/recx
cd recx
go build -o recx main.go
sudo mv recx /usr/local/bin/
```

<br>
<br>

<pre>
options:
  -h, -help    show help message
  -v           show version
  -t           scan timeout (default 120s)
  -w           workers (default 20)
  -d           max depth (default 8)
</pre>

<br>
<br>

`example commands`

```bash
cat urls.txt | recx
```
```bash
subfinder -d example.com -all -recursive -silent | recx
```
```bash
cat urls.txt | httpx -silent | recx | nuclei -t xss.yaml -o nuclei-xss.txt
```
```bash
echo "example.com" | recx | grep "'<" | tee xss.txt
```

<br>
<br>

`example output`
<pre>
https://example.com?param=REFLECTED (unfiltered:'<>$)
https://example.com/page?id=REFLECTED (unfiltered:<>'"})
</pre>


<br>
<br>

`troubleshooting`
- ensure target is accessible
- check your internet connection
- verify URL format (http:// or https://)
- increase timeout for large domains (-t flag)
- adjust worker count for better performance (-w flag)

<br>
<br>
<br>
<p align="center">
Made with <3 by <a href="https://github.com/1hehaq">@1hehaq</a>
<br>
Follow me on <a href="https://twitter.com/1hehaq">𝕏</a>
</p>
