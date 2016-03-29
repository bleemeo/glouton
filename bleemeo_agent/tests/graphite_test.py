import bleemeo_agent.graphite


def test_graphite_split_line():

    # Simple collectd metric
    (metric, value, timestamp) = bleemeo_agent.graphite.graphite_split_line(
        b'hostname_example_com.df-boot.df_complex-used 119843840 1459257933',
    )
    assert metric == 'hostname_example_com.df-boot.df_complex-used'
    assert value == 119843840.0
    assert timestamp == 1459257933.0

    # Simple telegraf metric
    (metric, value, timestamp) = bleemeo_agent.graphite.graphite_split_line(
        b'telegraf.hostname.ext4./boot.disk.used 119843840 1459257420'
    )
    assert metric == 'telegraf.hostname.ext4./boot.disk.used'
    assert value == 119843840.0
    assert timestamp == 1459257420.0

    # non-float value
    (metric, value, timestamp) = bleemeo_agent.graphite.graphite_split_line(
        b'telegraf.hostname.system.uptime_format "20 days, 23:26" 1459257790'
    )
    assert metric == 'telegraf.hostname.system.uptime_format'
    assert value == '20 days, 23:26'
    assert timestamp == 1459257790

    # Space in metric name
    (metric, value, timestamp) = bleemeo_agent.graphite.graphite_split_line(
        b'telegraf.hostname.elasticsearch.172_17_0_5.uVowpVl3RmO_S22rVTgWBA.'
        b'Thomas Halloway.elasticsearch_indices.percolate_current 0 1459257790'
    )
    assert metric == ('telegraf.hostname.elasticsearch.172_17_0_5.'
                      'uVowpVl3RmO_S22rVTgWBA.Thomas Halloway.'
                      'elasticsearch_indices.percolate_current'
                      )
    assert value == 0.0
    assert timestamp == 1459257790.0
