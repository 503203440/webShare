# 开放某个目录通过web共享

* 使用方法

```shell
webShare -addr :8080 -root ./ -username root -password password1234556
```
>
> * -addr 监听地址（默认：:8080）
> * -root 共享目录（默认当前shell目录）
> * -username 用户名（可选）
> * -password 密码（可选）
>

* 构建
  
```shell
# windows
go build --trimpath --ldflags "-w -s" -o webShare.exe

# linux amd64
SET GOOS=linux&& SET GOARCH=amd64&& go build --trimpath --ldflags="-w -s" -o webShare_linux_amd64

```

