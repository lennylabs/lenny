FROM maven:3.9-eclipse-temurin-21 AS b
WORKDIR /src
COPY pom.xml .
COPY src src
RUN mvn -q -B package dependency:copy-dependencies -DoutputDirectory=target/lib -DskipTests
FROM eclipse-temurin:21-jdk
COPY --from=b /src/target/spike-1.jar /app/spike.jar
COPY --from=b /src/target/lib /app/lib
