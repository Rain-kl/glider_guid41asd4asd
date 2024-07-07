# 通过【Glider】将机场节点作为爬虫代理池

> Convert nodes into a spider proxy pool that switches IP every second

## 介绍

当我们在使用爬虫时对一个网站进行多次爬取爬虫可能会被封禁，一般是针对IP进行封锁，所以如果我们切换IP就可以正常爬取了，于是我们就会用到代理池，目前代理池一个是免费的，但质量普遍较差，还有一个是付费的，费用普遍较高，超过我们的需求

最经济实惠的方法就是用机场的节点，配合Glider将其做为代理池使用

本项目主要介绍glider的作为代理池的使用，并没有参与glider项目的开发，如遇到glider的问题请到glider开发者下提issue
本文仅探讨分享技术供大家学习，不提供节点获取途径。

## 安装

下载Glider，github地址：[Release v0.16.3 · nadoo/glider (github.com)](https://github.com/nadoo/glider/releases/tag/v0.16.3)选择合适的版本

以下就是Glider的目录结构

![Snipaste_2023-06-11_09-54-04](https://github.com/Rain-kl/glider_guid41asd4asd/blob/master/img/Snipaste_2023-06-11_09-54-04.png)

## 创建配置文件

运行glider需要创建配置文件，配置文件模板在/config/examples文件内

这里我的配置名为glider.config，放置在glider.exe的同级目录下

以下是我的配置信息

```bash
# Verbose mode, print logs
verbose=True  #允许打印日志
listen=:8443  #监听端口

strategy=rr   #节点选择策略

check=http://www.msftconnecttest.com/connecttest.txt#expect=200   #延迟测试地址
checkinterval=60  #测试时间间隔

#节点信息
forward=...
forward=...
forward=...
```

### 节点选择策略

#### 轮询模式：rr

> Round Robin mode: rr

轮询模式是一种负载均衡算法，它会依次将请求分配到各个服务器上，直至所有服务器被均衡使用。该算法简单易实现，适用于服务器性能差异不大的情况。

简单来说就是IP换的特别快

#### 高可用模式：ha

> High Availability mode: ha

高可用模式指的是在服务器宕机或出现其他故障时，系统可以自动切换到备用服务器上，保证服务的持续可用性。这种模式常用于对服务可用性要求较高的场景，如金融、医疗等领域。

简单来说就是节点挂了才换

#### 延迟为基础的高可用模式：lha

> Latency based High Availability mode: lha

延迟为基础的高可用模式是一种结合了高可用与负载均衡思想的算法，它会根据服务器的延迟情况来判断是否需要切换到备用服务器上。当主服务器的延迟明显高于备用服务器时，系统会自动切换到备用服务器上以减少用户感知的延迟。

简单来说就是始终选择最低延迟的节点

#### 目标哈希模式：dh

>  Destination Hashing mode: dh

目标哈希模式是一种负载均衡算法，它会根据请求的某些关键特征（如来源 IP、URL等）计算哈希值，并将请求分配到相应的服务器上。该算法可以保证同一请求的多次访问会被路由到同一台服务器上，适用于需要对用户进行会话管理的场景。

打个比方来说就是访问百度的所有流量都交给一个节点，访问微软的所有流量交给另一个节点

### 节点配置信息

* 注意：以下内容随时可能被和谐

以下是glider对各协议的支持

![Snipaste_2023-06-11_10-17-31](https://github.com/Rain-kl/glider_guid41asd4asd/blob/master/img/Snipaste_2023-06-11_10-17-31.png)



如果你用的是“Clash for Windows”，可以打开配置目录C:\Users\你用户名\\.config\clash\profiles

![Snipaste_2023-06-11_10-15-18](https://github.com/Rain-kl/glider_guid41asd4asd/blob/master/img/Snipaste_2023-06-11_10-15-18.png)

用记事本打开yml，然后复制到我提供的转换器内，后续教程也放在那里了

## 运行Glider

在同级目录下打开cmd输入glider -config glider.conf

或者创建run.cmd文件，在里面输入glider -config glider.conf来执行程序

![Snipaste_2023-06-11_10-50-35](https://github.com/Rain-kl/glider_guid41asd4asd/blob/master/img/Snipaste_2023-06-11_10-50-35.png)

这样glider就运行起来了

## 使用Glider

## 爬虫

在request加入proxy参数，指定socks5协议端口指向之前配置文件里设置的端口

```python
$ pip install requests[socks]
proxies = {
    'http': 'socks5://127.0.0.1:8443',
    'https': 'socks5://127.0.0.1:8443'
}
```

## 上网

在“Clash for Windows”中新增配置信息，协议选择socks5，地址本地，端口为glider的端口，具体配置规范参考官方文档这里不做过多赘述，这里推荐使用lha策略，始终选择延迟最低的节点

以下是使用rr策略的ip变换情况

![Snipaste_2023-06-11_11-00-37](https://github.com/Rain-kl/glider_guid41asd4asd/blob/master/img/Snipaste_2023-06-11_11-00-37.png)


# 节点转换程序【parse.py】的使用
> 建议使用pycharm或vscode运行，需要安装一些第三方库
1. 将clash内配置文件全部复制到config.yml内
2. 运行订阅转换.py


以下是节点的规范

> trojan://密码@主机地址:端口?cert=PATH&key=PATH
没有配置过trojan协议的节点，故不做讨论

> vmess://加密方式:uuid@主机地址:端口?alterID=
{ name: '香港', type: vmess, server: 'xxx.cn', port: 123, uuid: ac005860, alterId: 0, cipher: auto, udp: true }
根据实测，加密方式填写auto程序无法执行，所以我统一按照none处理

> ss://加密方式:密码@主机地址:端口
{'name': '新加坡', 'type': 'ss', 'server': 'xxx.cn', 'port': 123, 'cipher': 'chacha20-ietf-poly1305', 'password': 'password', 'udp': True}

节点转换程序写的比较简单，只能转换以上三种节点，如果有其他节点格式，可以自行修改代码，欢迎各位大佬提PR


# 伪分布式爬虫参考模板
改模板位于`distributed_crawler`文件夹内

使用前需要将glider可执行程序放置在`distributed_crawler/core`文件夹下

glider.conf为基础模板文件，内部仅添加forward节点，策略和配置请前往代码修改
