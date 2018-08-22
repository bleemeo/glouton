from ctypes import *
import time

lib = cdll.LoadLibrary(
    "../agentgo/cabi/cabi.so")


def wrap_function(lib, funcname, restype, argtypes):
    """Simplify wrapping ctypes functions"""
    func = lib.__getattr__(funcname)
    func.restype = restype
    func.argtypes = argtypes
    return func


def functionTest():
    lib.FunctionTest.argtypes = []
    print("Functiontest() = %d" % lib.FunctionTest())


class MetricPoint(Structure):
    _fields_ = [('name', c_char_p),
                ('tag', POINTER(c_int)),
                ('tag_count', c_int),
                ('chart', c_char_p),
                ('unit', c_int),
                ('value', c_float)]


class MetricVector(Structure):
    _fields_ = [('metric_point', POINTER(MetricPoint)),
                ('metric_point_count', c_int)]


initMemoryCollector = wrap_function(lib, 'InitMemoryCollector', int, None)
gather = wrap_function(lib, 'Gather', MetricVector, [c_int, ])
memory_collector_id = initMemoryCollector()
while True:
    metrics_vector = gather(memory_collector_id)
    print("\n-------------------------------------------------")
    for i in range(0, metrics_vector.metric_point_count):
        metric_point = (metrics_vector.metric_point[i])
        print(
            "{}: {}".format(
                metric_point.name, metric_point.value
            )
        )
    time.sleep(2)
