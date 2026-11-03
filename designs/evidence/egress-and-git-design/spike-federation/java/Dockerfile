FROM maven:3.9-eclipse-temurin-21
WORKDIR /j
COPY pom.xml .
RUN V=$(curl -fsS https://repo1.maven.org/maven2/software/amazon/awssdk/bom/maven-metadata.xml | sed -n 's:.*<release>\(.*\)</release>.*:\1:p') && echo "bom $V" && sed -i "s/AWSV/$V/" pom.xml && mvn -q dependency:copy-dependencies -DoutputDirectory=/j/lib && ls /j/lib | grep -E '^(dynamodb|sts|auth|regions|sdk-core)-' > /versions.txt
