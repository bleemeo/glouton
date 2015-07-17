
import abc

import six


@six.add_metaclass(abc.ABCMeta)
class PluginV1Base(object):
    """ Base class for plugins version 1
    """

    def __init__(self, core):
        self.core = core

    @abc.abstractmethod
    def dependencies_present(self):
        """ Check for dependencies and return True if available
        """

    def collectd_configure(self):
        """ Do action needed to configure collectd

            Return string with section to add in collectd.conf
        """
        return None

    def collectd_rename_metric(self, name, timestamp, value):
        """ Return the canonical name for given metric
        """
        return None

    def list_checks(self):
        """ Return list of checks to run.

            The list contains 4-tuple with (short_name, description,
                check_command, tcp_port) where:

            * short_name is a short unique name. It should not contains space
              or special character (avoid ".", "/", ...) It is used to name the
              measurement.
            * description is a description for human so we can figure what this
              check does.
            * check_command point to a programm (and it's argument). The
              pointed programm should behave like a nagios check.
            * tcp_port is the TCP port associated with this check. It could
              be None.
        """
        return []
