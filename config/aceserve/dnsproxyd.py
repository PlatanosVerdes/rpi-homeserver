import socket
import struct
import os
import stat
import dns.resolver
import threading
from concurrent.futures import ThreadPoolExecutor

SOCKET_PATH = '/dev/socket/dnsproxyd'
BACKLOG = 128
MAX_WORKERS = 32


def handle_query(client_sock, resolver):
    with client_sock:
        try:
            query = client_sock.recv(1024)
            domain = query.split()[1].decode('utf-8')
        except Exception:
            return

        try:
            answer = resolver.resolve(domain)
        except Exception:
            answer = []

        if len(answer) > 0:
            response = create_addrinfo_response(answer[0].address)
        else:
            response = create_error_response()

        try:
            client_sock.sendall(response)
        except Exception:
            pass


def dnsproxyd_listener(resolver):
    os.makedirs(os.path.dirname(SOCKET_PATH), exist_ok=True)
    # A socket left behind by a killed engine would make bind() fail, and the
    # engine has no other resolver: it would start with DNS permanently dead.
    try:
        if stat.S_ISSOCK(os.stat(SOCKET_PATH).st_mode):
            os.remove(SOCKET_PATH)
    except FileNotFoundError:
        pass

    pool = ThreadPoolExecutor(max_workers=MAX_WORKERS, thread_name_prefix='dnsproxyd-query')
    try:
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as server_sock:
            server_sock.bind(SOCKET_PATH)
            server_sock.listen(BACKLOG)

            while True:
                client_sock, _ = server_sock.accept()
                # The engine resolves several hosts at once; answering them in
                # the accept loop made the extra connections fail outright.
                pool.submit(handle_query, client_sock, resolver)
    finally:
        pool.shutdown(wait=False)
        if os.path.exists(SOCKET_PATH):
            os.remove(SOCKET_PATH)


def create_addrinfo_response(ip):
    return struct.pack(
        '!4s I I I I I I I 4s I I I I',
        b"222",
        1, 0,
        2, 1, 6,
        16, 0x02000050, socket.inet_aton(ip),
        0, 0, 0, 0
    )


def create_error_response():
    return struct.pack(
        '!4s I I',
        b"401",
        4,
        0x7000000
    )


def dns_daemon(resolver):
    t = threading.Thread(target=dnsproxyd_listener, name="dnsproxyd", args=(resolver,))
    t.daemon = True
    t.start()
    return t


if __name__ == "__main__":
    t = dns_daemon(dns.resolver.Resolver())
    t.join()
