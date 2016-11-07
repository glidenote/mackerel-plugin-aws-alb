mackerel-plugin-aws-alb
=======================

AWS AWS Application Load Balancer custom metrics plugin for mackerel.io agent.

## Synopsis

```shell
mackerel-plugin-aws-alb [-lbname=<aws-application-load-blancer-name>] [-tgname=<targetgroup-name>] [-region=<aws-region>] [-access-key-id=<id>] [-secret-access-key=<key>] [-tempfile=<tempfile>]
```
* if you run on an ec2-instance, you probably don't have to specify `-region`
* if you run on an ec2-instance and the instance is associated with an appropriate IAM Role, you probably don't have to specify `-access-key-id` & `-secret-access-key`

## AWS IAM Policy
the credential provided manually or fetched automatically by IAM Role should have the policy that includes actions, 'cloudwatch:GetMetricStatistics' and 'cloudwatch:ListMetrics'

## Example of mackerel-agent.conf

```
[plugin.metrics.aws-alb]
command = "/path/to/mackerel-plugin-aws-alb"
```
