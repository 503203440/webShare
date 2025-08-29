# 开放某个目录通过web共享

* 使用方法

```shell
webShare -addr :8080 -root ./ -username root -password password1234556
```
>
> * -addr 监听地址
> * -root 共享目录
> * -username 用户名（可选）
> * -password 密码（可选）
>

* 构建
  
```shell
go build --trimpath --ldflags "-w -s" -o webShare.exe
```

