import yaml
import json


def parse_config(array: list):
    ss = []
    # {'name': '泰国', 'type': 'ss', 'server': 'xxx.cn', 'port': 123, 'cipher': 'chacha20-ietf-poly1305', 'password': 'password', 'udp': True}
    vmess = []
    # { name: '香港', type: vmess, server: 'xxx.cn', port: 123, uuid: ac005860, alterId: 0, cipher: auto, udp: true }
    for node in array:
        if node['type'] == 'ss':
            node = f"{node['type']}://{node['cipher']}:{node['password']}@{node['server']}:{node['port']}#{node['name']}"
            ss.append(node)
        elif node['type'] == 'vmess':
            node = f"{node['type']}://none:{node['uuid']}@{node['server']}:{node['port']}?alterID={node['alterId']}"
            vmess.append(node)
    for node in ss:
        print(f'forward={node}')
    print('-------------------')
    for node in vmess:
        print(f'forward={node}')


if __name__ == '__main__':
    with open('config.yml', 'r', encoding='utf-8') as f:
        config = yaml.load(f, Loader=yaml.FullLoader)
        list_array = config['proxies']
        parse_config(list_array)
