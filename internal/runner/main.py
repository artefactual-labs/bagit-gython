import json
import multiprocessing
import sys

from bagit import Bag, make_bag
from bagit import BagError


class Runner:
    def __init__(self, req):
        self.req = req

    @property
    def cmd(self):
        return self.req.get("cmd")

    @property
    def opts(self):
        return self.req.get("opts")

    def run(self):
        resp = {}

        try:
            if self.cmd == "validate":
                resp = self.validate(self.opts)
            elif self.cmd == "make":
                resp = self.make(self.opts)
            else:
                raise Exception("Unknown command")
        except BaseException as err:
            resp["err"] = str(err)

        return json.dumps(resp)

    def validate(self, opts):
        bag = Bag(opts.get("path"))
        bag.validate(processes=multiprocessing.cpu_count())
        return {"valid": True}

    def make(self, opts):
        bag_dir = opts.pop("path")
        bag = make_bag(bag_dir, **opts)
        return {"version": bag.version}


def main():
    while True:
        cmd = sys.stdin.readline()
        if not cmd:
            break

        req = json.loads(cmd)
        if req.get("cmd") == "exit":
            break

        result = Runner(req).run()

        sys.stdout.write(result + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
