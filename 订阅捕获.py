from urllib.parse import unquote

import requests
from loguru import logger
import base64
import re


class SubCapture:
    def __init__(self, url):
        self.url = url

    def get_source(self):
        try:
            node_encryption = requests.get(self.url).text
            logger.success('订阅捕获成功')
            return node_encryption
        except Exception as e:
            logger.error(e)
            exit(1)

    def decrypt(self, node_encryption):
        return base64.b64decode(node_encryption).decode()

    def re_decrypt(self, context):
        detail = ''.join(re.findall(r'ss://(.*?)@', context))
        if detail:
            if len(detail) % 3 != 0:
                detail += '=' * (3 - len(detail) % 3)
            dec = base64.urlsafe_b64decode(detail).decode()
            return re.sub(r'ss://(.*)@', 'ss://' + dec + '@', context)


if __name__ == '__main__':
    url = input('url: ')
    s = SubCapture(url)
    source = s.get_source()
    nodelist = s.decrypt(source).split('\n')
    for node in nodelist:
        if node:
            node = s.re_decrypt(node)
            try:
                print(f'forward={node}')
            except Exception as e:
                print(f'forward={unquote(node)}')
