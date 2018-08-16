from ctypes import *

lib = cdll.LoadLibrary(
    "../agentgo/cabi/cabi.so")

lib.FunctionTest.argtypes = []
print("memory.Functiontest() = %d" % lib.FunctionTest())
