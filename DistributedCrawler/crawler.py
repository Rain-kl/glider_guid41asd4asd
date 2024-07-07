import re

from tqdm.asyncio import tqdm_asyncio

import json
import httpx
import asyncio
import random
import functools
from loguru import logger
from typing import Union, Callable

from typing_extensions import Any


class Retry:

    @staticmethod
    def async_retry(
            retries: int = 3,
            delay: float = 0,
            _log: str = 'Running',
            success_log: bool = False,
            throw_error: bool = False
    ) -> Callable:
        """
        Attempt to call a function, if it fails, try again with a specified delay.

        :param retries:  The max amount of retries you want for the function call
        :param delay:  The delay (in seconds) between each function retry
        :param _log:  The log message
        :param success_log:  Whether to log success
        :param throw_error:  Whether to throw error
        :return:  The result of the function call
        """

        def decorator(func) -> Callable:

            @functools.wraps(func)
            async def async_wrapper(*args, **kwargs) -> Any:
                for count in range(1, retries + 1):
                    try:
                        result = await func(*args, **kwargs)
                        if success_log:
                            logger.success(f"{_log} Success: {func.__name__} {json.dumps([str(i) for i in args])}")
                        return result
                    except AssertionError as e:
                        raise e
                    except httpx.ReadTimeout as e:
                        logger.error(f"{_log} __{func.__name__}__ Timeout {count}: {str(e)}")
                    except Exception as e:
                        logger.error(f"{_log} __{func.__name__}__ Failed {count}: {str(e)}")
                    finally:
                        await asyncio.sleep(delay)

                logger.critical(
                    f"{_log} __{func.__name__}__ Failed: Retried {retries} , log：{func.__name__} {json.dumps([str(i) for i in args])}")
                if throw_error:
                    raise Exception(
                        f"{_log} __{func.__name__}__ Failed: Retried{retries}, log：{func.__name__} {json.dumps([str(i) for i in args])}")

            return async_wrapper

        return decorator


class ProxyConfig:
    def __init__(
            self,
            host: str = None,
            port: Union[str, int, list] = None
    ):
        """
        :param host: 127.0.0.1
        :param port: 7890
        """
        self.host = host
        self.port = port

    def get_proxy(self):
        if isinstance(self.port, list):
            port = random.choice(self.port)
        else:
            port = self.port
        HTTP_PROXY = f"http://{self.host}:{port}"
        return {'http://': HTTP_PROXY, 'https://': HTTP_PROXY}


class AsyncSpider:
    def __init__(
            self,
            headers: dict,
            proxies: dict = None,
            timeout: int = 10,
            cookie_pool: list = None,
            proxy_pool: ProxyConfig = None
    ):
        self.headers = headers
        self.proxies = proxies
        self.timeout = timeout
        self.cookie_pool = cookie_pool
        self.proxy_pool = proxy_pool

    async def _get_request(self, url: str, params=None) -> httpx.Response:
        if self.cookie_pool:
            self.headers['Cookie'] = random.choice(self.cookie_pool)
        if self.proxy_pool:
            self.proxies = self.proxy_pool.get_proxy()
        async with httpx.AsyncClient(proxies=self.proxies, verify=False) as client:
            response = await client.get(url, headers=self.headers, timeout=self.timeout, params=params)
            return response

    async def _post_request(self, url: str, data=None, _json=None) -> httpx.Response:
        if self.cookie_pool:
            self.headers['Cookie'] = random.choice(self.cookie_pool)
        if self.proxy_pool:
            self.proxies = self.proxy_pool.get_proxy()
        async with httpx.AsyncClient(proxies=self.proxies, verify=False) as client:
            response: httpx.Response = await client.post(url, headers=self.headers, timeout=self.timeout, data=data,
                                                         json=_json)
            return response


class IP125(AsyncSpider):
    def __init__(self, proxy_pool: ProxyConfig = None):
        super().__init__(
            headers={
                'user-agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36'
            },
            proxy_pool=proxy_pool
        )

    async def get_foreign_ip(self) -> str:
        url = 'https://jsonp-ip.com/?callback=jsonp_callback_1558'
        response = await self._get_request(url)

        return ''.join(re.findall('\d+.\d+.\d+.\d+', response.text))

    async def get_domestic_ip(self) -> str:
        url = 'https://whois.pconline.com.cn/ipJson.jsp?callback=jsonp_callback_54465'
        response = await self._get_request(url)
        return ''.join(re.findall('\d+.\d+.\d+.\d+', response.text))

    async def ip_detail(self, ip) -> dict:
        url = 'https://ip125.com/api/{}?lang=zh-CN'.format(ip)
        response = await self._get_request(url)
        dic_re = response.json()
        return dic_re


async def run_ip125():
    proxy = ProxyConfig(
        host='127.0.0.1',
        port=50010
    )
    crawler = IP125(
        proxy_pool=proxy
    )
    fip = await crawler.get_foreign_ip()
    response = await crawler.ip_detail(fip)
    return response


async def main():
    tasks = [asyncio.create_task(run_ip125()) for i in range(50)]
    rsp = await tqdm_asyncio.gather(*tasks)
    for i in rsp:
        print(i)


if __name__ == '__main__':
    # asyncio.run(run_ip125())
    asyncio.run(main())
